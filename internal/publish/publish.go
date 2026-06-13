// Package publish runs periodic sensor and camera reads, writing results into
// the snapshot store and the camera frame buffer.
package publish

import (
	"log"
	"sync"
	"time"

	"github.com/iot-root/garden-of-eden/internal/hw"
	"github.com/iot-root/garden-of-eden/internal/state"
)

type Publisher struct {
	dev        hw.Devices
	store      *state.Store
	frames     *state.Frames
	interval   time.Duration
	onOverTemp func(bool) // optional; nil to skip over-temp reporting
	done       chan struct{}
	stopOnce   sync.Once
}

func New(dev hw.Devices, store *state.Store, frames *state.Frames, interval time.Duration, onOverTemp func(bool)) *Publisher {
	return &Publisher{dev: dev, store: store, frames: frames, interval: interval, onOverTemp: onOverTemp, done: make(chan struct{})}
}

func (p *Publisher) publishOnce() {
	if p.dev.Env != nil {
		if t, h, err := p.dev.Env.Read(); err == nil {
			p.store.SetTemperature(t)
			p.store.SetHumidity(h)
		} else {
			log.Printf("env read: %v", err)
		}
	}
	if p.dev.PCBTemp != nil {
		if t, err := p.dev.PCBTemp.Temperature(); err == nil {
			p.store.SetPCBTemp(t)
		}
		if p.onOverTemp != nil {
			if over, err := p.dev.PCBTemp.OverTemp(); err == nil {
				p.onOverTemp(over)
			}
		}
	}
	if p.dev.Distance != nil {
		if cm, err := p.dev.Distance.MeasureCM(); err == nil {
			p.store.SetWaterLevel(cm)
		}
	}
	if p.dev.Power != nil {
		if r, err := p.dev.Power.Read(); err == nil {
			p.store.SetPumpPower(state.PumpPower{BusVoltage: r.BusVoltage, Current: r.Current, Power: r.Power})
		}
	}
}

func (p *Publisher) captureOnce() {
	if p.dev.UpperCamera != nil {
		if b, err := p.dev.UpperCamera.Capture(); err == nil {
			p.frames.SetUpper(b)
		} else {
			log.Printf("upper camera: %v", err)
		}
	}
	if p.dev.LowerCamera != nil {
		if b, err := p.dev.LowerCamera.Capture(); err == nil {
			p.frames.SetLower(b)
		} else {
			log.Printf("lower camera: %v", err)
		}
	}
}

// Run publishes sensors immediately then every interval.
func (p *Publisher) Run() {
	p.publishOnce()
	if p.interval <= 0 {
		log.Printf("publish: invalid sensor interval %v, defaulting to 30m", p.interval)
		p.interval = 30 * time.Minute
	}
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()
	for {
		select {
		case <-p.done:
			return
		case <-ticker.C:
			p.publishOnce()
		}
	}
}

// RunCameras captures on its own (slower) interval.
func (p *Publisher) RunCameras(interval time.Duration) {
	p.captureOnce()
	if interval <= 0 {
		log.Printf("publish: invalid camera interval %v, defaulting to 1h", interval)
		interval = time.Hour
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-p.done:
			return
		case <-ticker.C:
			p.captureOnce()
		}
	}
}

func (p *Publisher) Stop() { p.stopOnce.Do(func() { close(p.done) }) }
