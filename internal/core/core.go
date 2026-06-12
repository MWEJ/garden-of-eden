// Package core is the single-writer state machine. All hardware mutation goes
// through one goroutine; inputs submit Commands and the core writes results to
// the snapshot store.
package core

import (
	"log"

	"github.com/iot-root/garden-of-eden/internal/hw"
	"github.com/iot-root/garden-of-eden/internal/state"
)

type Target int

const (
	TargetLight Target = iota
	TargetPump
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
	lightLevel int
	pumpLevel  int
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

func (c *Core) Submit(cmd Command) { c.cmds <- cmd }
func (c *Core) Stop()              { close(c.done) }

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
		if cmd.Value > 0 {
			c.pumpLevel = cmd.Value
		}
		if err := c.dev.Pump.SetSpeed(c.pumpLevel); err != nil {
			log.Printf("pump on: %v", err)
			return
		}
		c.store.SetPump(true, c.pumpLevel)
	case ActionOff:
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
