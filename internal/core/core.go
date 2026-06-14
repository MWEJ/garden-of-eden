// Package core is the single-writer state machine. All hardware mutation goes
// through one goroutine; inputs submit Commands and the core writes results to
// the snapshot store.
package core

import (
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/iot-root/garden-of-eden/internal/hw"
	"github.com/iot-root/garden-of-eden/internal/state"
)

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
	pumpTimer          *time.Timer // core-goroutine only
	pumpStateFile      string      // core-goroutine only after startup
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
	switch cmd.Action {
	case ActionOn:
		if cmd.Value > 0 {
			c.lightLevel = cmd.Value
		}
		if err := c.dev.Light.SetBrightness(c.lightLevel); err != nil {
			log.Printf("light on: %v", err)
			return
		}
		c.store.SetLight(true, c.lightLevel)
	case ActionOff:
		if err := c.dev.Light.Off(); err != nil {
			log.Printf("light off: %v", err)
			return
		}
		c.store.SetLight(false, c.lightLevel)
	case ActionSetLevel:
		c.lightLevel = cmd.Value
		if err := c.dev.Light.SetBrightness(cmd.Value); err != nil {
			log.Printf("light level: %v", err)
			return
		}
		c.store.SetLight(cmd.Value > 0, cmd.Value)
	}
}

func (c *Core) applyPump(cmd Command) {
	switch cmd.Action {
	case ActionOn:
		threshold := c.waterLow()
		if threshold > 0 {
			cm, err := c.measureDistance()
			if err != nil {
				// Distance read failed. Record sensor health, then apply policy.
				c.store.SetWaterSensorOK(false)
				if c.blockOnError() {
					log.Printf("pump on: distance read failed, blocking (fail-closed): %v", err)
					c.flashLights()
					return
				}
				log.Printf("pump on: distance read failed, proceeding (fail-open): %v", err)
			} else {
				c.store.SetWaterSensorOK(true)
				if cm > threshold {
					c.store.SetWater(threshold, true) // low=true
					c.store.SetWaterSensorOK(true)    // SetWater zeroes SensorOK; restore it
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
			log.Printf("pump on: %v", err)
			return
		}
		if c.pumpStateFile != "" {
			if err := writePumpState(c.pumpStateFile, time.Now()); err != nil {
				log.Printf("pump state persist (continuing): %v", err)
			}
		}
		c.armPumpFailsafe()
		c.store.SetPump(true, c.pumpLevel)
	case ActionOff:
		c.disarmPumpFailsafe()
		if c.pumpStateFile != "" {
			if err := clearPumpState(c.pumpStateFile); err != nil {
				log.Printf("pump state clear (continuing): %v", err)
			}
		}
		if err := c.dev.Pump.Off(); err != nil {
			log.Printf("pump off: %v", err)
			return
		}
		c.store.SetPump(false, c.pumpLevel)
	case ActionSetLevel:
		c.pumpLevel = cmd.Value
		if err := c.dev.Pump.SetSpeed(cmd.Value); err != nil {
			log.Printf("pump level: %v", err)
			return
		}
		c.store.SetPump(cmd.Value > 0, cmd.Value)
	}
}

func (c *Core) applyOverTemp(cmd Command) {
	alert := cmd.Value == 1
	c.store.SetOverTemp(alert)
	if alert && c.cutLightOnTemp() && c.dev.Light != nil {
		_ = c.dev.Light.Off()
		c.store.SetLight(false, c.lightLevel)
	}
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
		c.Submit(Command{Target: TargetPump, Action: ActionOff})
		log.Printf("pump failsafe: forced off after %s", maxRT)
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
		log.Printf("pump state read at startup (clearing): %v", err)
		if cerr := clearPumpState(path); cerr != nil {
			log.Printf("pump state clear at startup: %v", cerr)
		}
		return 0
	}
	if !ok {
		return 0
	}
	if ShouldForceOff(startedAt, now, maxRuntime) {
		log.Printf("pump state: previous run exceeded max runtime (%s); forcing pump off", maxRuntime)
		if c.dev.Pump != nil {
			if oerr := c.dev.Pump.Off(); oerr != nil {
				log.Printf("pump off at startup: %v", oerr)
			}
		}
		c.store.SetPump(false, c.pumpLevel)
		if cerr := clearPumpState(path); cerr != nil {
			log.Printf("pump state clear at startup: %v", cerr)
		}
		return 0
	}
	return maxRuntime - now.Sub(startedAt)
}
