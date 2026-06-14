// Package core is the single-writer state machine. All hardware mutation goes
// through one goroutine; inputs submit Commands and the core writes results to
// the snapshot store.
package core

import (
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/iot-root/garden-of-eden/internal/events"
	"github.com/iot-root/garden-of-eden/internal/hw"
	"github.com/iot-root/garden-of-eden/internal/state"
)

// fadeStepCap, when > 0, clamps each fade step's sleep. Production leaves it 0
// (use the natural per-step delay); tests set it small to run fast.
var fadeStepCap time.Duration

type Target int

const (
	TargetLight Target = iota
	TargetPump
	TargetOverTemp
)

type Action int

const (
	ActionOn Action = iota
	ActionOff
	ActionSetLevel // Value is the new brightness/speed percent
)

type Command struct {
	Target Target
	Action Action
	Value  int

	// fadeGen tags a command emitted by a fade goroutine with the fade
	// generation that produced it. Zero means "not a fade step" (manual/
	// scheduler command). The core applies a fade SetLevel only when fadeGen
	// equals the currently-active generation, so a step that was already
	// in-flight when a newer command (off/on/new fade) cancelled the ramp is
	// dropped on the core goroutine — closing the cancel/Submit race.
	fadeGen uint64
}

type Core struct {
	dev        hw.Devices
	store      *state.Store
	cmds       chan Command
	done       chan struct{}
	stopOnce   sync.Once
	lightLevel int
	pumpLevel  int

	cfgMu              sync.Mutex // guards runtime-tunable config: waterLowCM, pumpMaxRuntime, cutLightOnOverTemp, blockOnSensorError
	waterLowCM         float64
	pumpMaxRuntime     time.Duration
	cutLightOnOverTemp bool
	blockOnSensorError bool
	pumpTimer          *time.Timer   // core-goroutine only
	pumpStateFile      string        // core-goroutine only after startup
	fadeCancel         chan struct{} // guarded by cfgMu; closed to cancel an active fade
	fadeGen            uint64        // guarded by cfgMu; active fade generation (0 = none)

	rec *events.Recorder // guarded by nil-safe Recorder.Record; set via SetEvents
}

func New(dev hw.Devices, store *state.Store) *Core {
	return &Core{
		dev:        dev,
		store:      store,
		cmds:       make(chan Command, 16),
		done:       make(chan struct{}),
		lightLevel: 50,
		pumpLevel:  100,
	}
}

// SetEvents wires an event recorder into the core. Must be called before
// Run (no locking needed — Run and Submit are not yet in flight).
func (c *Core) SetEvents(rec *events.Recorder) { c.rec = rec }

func (c *Core) Submit(cmd Command) {
	select {
	case c.cmds <- cmd:
	case <-c.done:
	}
}
func (c *Core) Stop() { c.stopOnce.Do(func() { close(c.done) }) }

// SetWaterLowCM sets the water-low threshold (cm of measured distance above
// which water is considered too low and the pump is blocked). May be called at
// runtime (e.g. from the REST handler) concurrently with the core goroutine, so
// the field is guarded by cfgMu.
func (c *Core) SetWaterLowCM(cm float64) {
	c.cfgMu.Lock()
	c.waterLowCM = cm
	c.cfgMu.Unlock()
	c.store.SetWater(cm, false)
}
func (c *Core) SetPumpMaxRuntime(d time.Duration) {
	c.cfgMu.Lock()
	c.pumpMaxRuntime = d
	c.cfgMu.Unlock()
}
func (c *Core) SetCutLightOnOverTemp(b bool) {
	c.cfgMu.Lock()
	c.cutLightOnOverTemp = b
	c.cfgMu.Unlock()
}
func (c *Core) SetBlockOnSensorError(b bool) {
	c.cfgMu.Lock()
	c.blockOnSensorError = b
	c.cfgMu.Unlock()
}

// SetPumpStateFile sets the path used to persist the pump-on start time for
// restart-enforced failsafe. Call once at startup before the core handles any
// pump command; thereafter it is read only on the core goroutine.
func (c *Core) SetPumpStateFile(path string) { c.pumpStateFile = path }

// waterLow returns the current threshold under cfgMu.
func (c *Core) waterLow() float64 {
	c.cfgMu.Lock()
	defer c.cfgMu.Unlock()
	return c.waterLowCM
}

// pumpMaxRT returns the current pump max runtime under cfgMu.
func (c *Core) pumpMaxRT() time.Duration {
	c.cfgMu.Lock()
	defer c.cfgMu.Unlock()
	return c.pumpMaxRuntime
}

// cutLightOnTemp returns whether the light is cut on over-temp under cfgMu.
func (c *Core) cutLightOnTemp() bool {
	c.cfgMu.Lock()
	defer c.cfgMu.Unlock()
	return c.cutLightOnOverTemp
}

// blockOnError returns the sensor-error fail policy under cfgMu.
func (c *Core) blockOnError() bool {
	c.cfgMu.Lock()
	defer c.cfgMu.Unlock()
	return c.blockOnSensorError
}

func (c *Core) Run() {
	for {
		select {
		case <-c.done:
			return
		case cmd := <-c.cmds:
			c.apply(cmd)
		}
	}
}

func (c *Core) apply(cmd Command) {
	switch cmd.Target {
	case TargetLight:
		c.applyLight(cmd)
	case TargetPump:
		c.applyPump(cmd)
	case TargetOverTemp:
		c.applyOverTemp(cmd)
	}
}

func (c *Core) applyLight(cmd Command) {
	if cmd.Action != ActionSetLevel {
		// A fade drives the light via ActionSetLevel; only NON-fade commands
		// (manual on/off, or a new fade) cancel an in-progress ramp.
		c.cancelFade()
	}
	switch cmd.Action {
	case ActionOn:
		if cmd.Value > 0 {
			c.lightLevel = cmd.Value
		}
		if err := c.dev.Light.SetBrightness(c.lightLevel); err != nil {
			slog.Error("light on", "err", err)
			return
		}
		c.store.SetLight(true, c.lightLevel)
	case ActionOff:
		if err := c.dev.Light.Off(); err != nil {
			slog.Error("light off", "err", err)
			return
		}
		c.store.SetLight(false, c.lightLevel)
	case ActionSetLevel:
		// Drop a fade step whose generation is no longer active: a newer
		// command (off/on/new fade) cancelled this ramp after the step was
		// already enqueued. Checked here on the core goroutine — the only
		// place fadeGen and the device are both read — so there is no window
		// for a stale step to land after the cancelling command.
		if cmd.fadeGen != 0 && cmd.fadeGen != c.activeFadeGen() {
			return
		}
		c.lightLevel = cmd.Value
		if err := c.dev.Light.SetBrightness(cmd.Value); err != nil {
			slog.Error("light level", "err", err)
			return
		}
		c.store.SetLight(cmd.Value > 0, cmd.Value)
	}
}

func (c *Core) applyPump(cmd Command) {
	c.cancelFade() // any pump action cancels a light fade per Decision 4
	switch cmd.Action {
	case ActionOn:
		threshold := c.waterLow()
		if threshold > 0 {
			cm, err := c.measureDistance()
			if err != nil {
				// Distance read failed. Record sensor health, then apply policy.
				c.store.SetWaterSensorOK(false)
				if c.blockOnError() {
					slog.Error("pump on: distance read failed, blocking (fail-closed)", "err", err)
					c.flashLights()
					return
				}
				slog.Warn("pump on: distance read failed, proceeding (fail-open)", "err", err)
			} else {
				c.store.SetWaterSensorOK(true)
				if cm > threshold {
					c.store.SetWater(threshold, true) // low=true
					c.store.SetWaterSensorOK(true)    // SetWater zeroes SensorOK; restore it
					c.rec.Record("interlock_block", fmt.Sprintf("distance=%.1fcm threshold=%.1fcm", cm, threshold))
					c.flashLights()
					return
				}
				c.store.SetWater(threshold, false)
				c.store.SetWaterSensorOK(true) // SetWater zeroes SensorOK; restore it
			}
		}
		if cmd.Value > 0 {
			c.pumpLevel = cmd.Value
		}
		if err := c.dev.Pump.SetSpeed(c.pumpLevel); err != nil {
			slog.Error("pump on", "err", err)
			return
		}
		if c.pumpStateFile != "" {
			if err := writePumpState(c.pumpStateFile, time.Now()); err != nil {
				slog.Warn("pump state persist (continuing)", "err", err)
			}
		}
		c.armPumpFailsafe()
		c.store.SetPump(true, c.pumpLevel)
		c.rec.Record("pump_on", fmt.Sprintf("speed=%d", c.pumpLevel))
	case ActionOff:
		c.disarmPumpFailsafe()
		if c.pumpStateFile != "" {
			if err := clearPumpState(c.pumpStateFile); err != nil {
				slog.Warn("pump state clear (continuing)", "err", err)
			}
		}
		if err := c.dev.Pump.Off(); err != nil {
			slog.Error("pump off", "err", err)
			return
		}
		c.store.SetPump(false, c.pumpLevel)
		c.rec.Record("pump_off", "")
	case ActionSetLevel:
		c.pumpLevel = cmd.Value
		if err := c.dev.Pump.SetSpeed(cmd.Value); err != nil {
			slog.Error("pump level", "err", err)
			return
		}
		c.store.SetPump(cmd.Value > 0, cmd.Value)
	}
}

func (c *Core) applyOverTemp(cmd Command) {
	alert := cmd.Value == 1
	c.store.SetOverTemp(alert)
	if alert {
		c.rec.Record("overtemp", "")
		if c.cutLightOnTemp() && c.dev.Light != nil {
			_ = c.dev.Light.Off()
			c.store.SetLight(false, c.lightLevel)
		}
	} else {
		c.rec.Record("overtemp_clear", "")
	}
}

// ApplyLightFade ramps the light to `target` over fadeMin minutes. It cancels
// any in-progress fade, then launches a goroutine that submits stepped
// ActionSetLevel commands through the core (preserving single-writer mutation).
// Any subsequent light/pump command cancels the fade via cancelFade.
func (c *Core) ApplyLightFade(target, fadeMin int) {
	from := 0
	if c.dev.Light != nil {
		from = c.dev.Light.Brightness()
	}
	steps := fadeSteps(from, target, fadeMin)

	// Snapshot the step cap once on the caller's goroutine so the fade goroutine
	// never reads the package var concurrently (keeps -race clean; tests mutate
	// fadeStepCap before/after running a fade).
	stepCap := fadeStepCap

	cancel := make(chan struct{})
	c.cfgMu.Lock()
	if c.fadeCancel != nil {
		close(c.fadeCancel)
	}
	c.fadeCancel = cancel
	c.fadeGen++ // start a new generation; tags this fade's steps
	gen := c.fadeGen
	c.cfgMu.Unlock()

	go func() {
		for _, s := range steps {
			d := s.delay
			if stepCap > 0 && (d > stepCap || d == 0) {
				d = stepCap
			}
			if d > 0 {
				select {
				case <-cancel:
					return
				case <-c.done:
					return
				case <-time.After(d):
				}
			}
			select {
			case <-cancel:
				return
			default:
			}
			// Tag the step with this fade's generation. Even if the channel
			// close races with this Submit, the core drops the step when its
			// generation is no longer active (see applyLight ActionSetLevel).
			c.Submit(Command{Target: TargetLight, Action: ActionSetLevel, Value: s.level, fadeGen: gen})
		}
	}()
}

// cancelFade stops any active fade. Runs on the core goroutine (from applyLight/
// applyPump) so a manual command instantly supersedes a ramp. It both closes the
// cancel channel (stops the goroutine sleeping/looping) and clears the active
// generation (drops any step already enqueued behind the cancelling command).
func (c *Core) cancelFade() {
	c.cfgMu.Lock()
	if c.fadeCancel != nil {
		close(c.fadeCancel)
		c.fadeCancel = nil
	}
	c.fadeGen = 0
	c.cfgMu.Unlock()
}

// activeFadeGen returns the currently-active fade generation under cfgMu (0 when
// no fade is active).
func (c *Core) activeFadeGen() uint64 {
	c.cfgMu.Lock()
	defer c.cfgMu.Unlock()
	return c.fadeGen
}

func (c *Core) measureDistance() (float64, error) {
	if c.dev.Distance == nil {
		return 0, fmt.Errorf("no distance sensor")
	}
	return c.dev.Distance.MeasureCM()
}

func (c *Core) flashLights() {
	if c.dev.Light == nil {
		return
	}
	prev := c.dev.Light.Brightness()
	for i := 0; i < 3; i++ {
		_ = c.dev.Light.Off()
		time.Sleep(300 * time.Millisecond)
		_ = c.dev.Light.SetBrightness(100)
		time.Sleep(300 * time.Millisecond)
	}
	_ = c.dev.Light.SetBrightness(prev)
}

func (c *Core) armPumpFailsafe() {
	maxRT := c.pumpMaxRT()
	if maxRT <= 0 {
		return
	}
	c.disarmPumpFailsafe()
	c.pumpTimer = time.AfterFunc(maxRT, func() {
		c.rec.Record("pump_failsafe", fmt.Sprintf("after=%s", maxRT))
		c.Submit(Command{Target: TargetPump, Action: ActionOff})
		slog.Warn("pump failsafe: forced off", "after", maxRT)
	})
}

func (c *Core) disarmPumpFailsafe() {
	if c.pumpTimer != nil {
		c.pumpTimer.Stop()
		c.pumpTimer = nil
	}
}

// EnforcePumpRuntime is called once at startup to re-enforce the max-runtime
// failsafe across a crash/restart. If a persisted pump-on start time exists:
//   - if it has already run >= maxRuntime, the pump is driven OFF, the file is
//     cleared, and 0 is returned (caller should not arm a failsafe);
//   - otherwise the file is left in place and the REMAINING duration is
//     returned so the caller can arm a failsafe for that remainder.
//
// Returns 0 when there is no file (or it is unreadable). Best-effort: errors
// are logged, never fatal. Must run before c.Run handles any pump command.
//
// Why direct device access here is safe: this runs at startup before c.Run
// handles any pump command (no concurrent core-goroutine writer to race with),
// so it touches the device directly rather than via the command channel,
// avoiding a chicken-and-egg dependency on the command loop being live.
func (c *Core) EnforcePumpRuntime(path string, maxRuntime time.Duration, now time.Time) time.Duration {
	startedAt, ok, err := readPumpState(path)
	if err != nil {
		slog.Warn("pump state read at startup (clearing)", "err", err)
		if cerr := clearPumpState(path); cerr != nil {
			slog.Warn("pump state clear at startup", "err", cerr)
		}
		return 0
	}
	if !ok {
		return 0
	}
	if ShouldForceOff(startedAt, now, maxRuntime) {
		slog.Warn("pump state: previous run exceeded max runtime; forcing pump off", "maxRuntime", maxRuntime)
		if c.dev.Pump != nil {
			if oerr := c.dev.Pump.Off(); oerr != nil {
				slog.Error("pump off at startup", "err", oerr)
			}
		}
		c.store.SetPump(false, c.pumpLevel)
		if cerr := clearPumpState(path); cerr != nil {
			slog.Warn("pump state clear at startup", "err", cerr)
		}
		return 0
	}
	return maxRuntime - now.Sub(startedAt)
}
