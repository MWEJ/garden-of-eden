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

	cfgMu              sync.Mutex // guards runtime-tunable config: waterLowCM, pumpMaxRuntime, cutLightOnOverTemp
	waterLowCM         float64
	pumpMaxRuntime     time.Duration
	cutLightOnOverTemp bool
	pumpTimer          *time.Timer // core-goroutine only
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
			if err == nil && cm > threshold {
				c.store.SetWater(threshold, true) // low=true
				c.flashLights()
				return
			}
			c.store.SetWater(threshold, false)
		}
		if cmd.Value > 0 {
			c.pumpLevel = cmd.Value
		}
		if err := c.dev.Pump.SetSpeed(c.pumpLevel); err != nil {
			log.Printf("pump on: %v", err)
			return
		}
		c.armPumpFailsafe()
		c.store.SetPump(true, c.pumpLevel)
	case ActionOff:
		c.disarmPumpFailsafe()
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
