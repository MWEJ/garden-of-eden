// Package publish runs periodic sensor and camera reads, writing results into
// the snapshot store and the camera frame buffer.
package publish

import (
	"log/slog"
	"sync"
	"time"

	"github.com/iot-root/garden-of-eden/internal/health"
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
	tracker    *health.Tracker // nil is safe; Touch is only called when non-nil
}

func New(dev hw.Devices, store *state.Store, frames *state.Frames, interval time.Duration, onOverTemp func(bool)) *Publisher {
	return &Publisher{dev: dev, store: store, frames: frames, interval: interval, onOverTemp: onOverTemp, done: make(chan struct{})}
}

// SetHealthTracker wires a health tracker into the publisher. Must be called
// before Run. nil is accepted (disables health tracking).
func (p *Publisher) SetHealthTracker(tr *health.Tracker) { p.tracker = tr }

func (p *Publisher) publishOnce() {
	if p.dev.Env != nil {
		if t, h, err := p.dev.Env.Read(); err == nil {
			p.store.SetTemperature(t)
			p.store.SetHumidity(h)
			if p.tracker != nil {
				p.tracker.Touch("env")
			}
		} else {
			slog.Warn("env read failed", "err", err)
		}
	}
	if p.dev.PCBTemp != nil {
		if t, err := p.dev.PCBTemp.Temperature(); err == nil {
			p.store.SetPCBTemp(t)
			if p.tracker != nil {
				p.tracker.Touch("pcb_temp")
			}
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
			// Recompute water.low on every cycle so the snapshot stays fresh
			// even between pump-on attempts. Threshold == 0 means the interlock
			// is disabled; skip in that case so we don't overwrite a deliberate
			// SetWater call with a zero threshold.
			snap := p.store.Snapshot()
			if snap.Water.LowThresholdCM > 0 {
				low := cm > snap.Water.LowThresholdCM
				p.store.SetWater(snap.Water.LowThresholdCM, low)
			}
			if p.tracker != nil {
				p.tracker.Touch("distance")
			}
		}
	}
	if p.dev.Power != nil {
		if r, err := p.dev.Power.Read(); err == nil {
			p.store.SetPumpPower(state.PumpPower{BusVoltage: r.BusVoltage, Current: r.Current, Power: r.Power})
			if p.tracker != nil {
				p.tracker.Touch("pump_power")
			}
		}
	}
}

func (p *Publisher) captureOnce() {
	if p.dev.UpperCamera != nil {
		if b, err := p.dev.UpperCamera.Capture(); err == nil {
			p.frames.SetUpper(b)
		} else {
			slog.Warn("upper camera capture failed", "err", err)
		}
	}
	if p.dev.LowerCamera != nil {
		if b, err := p.dev.LowerCamera.Capture(); err == nil {
			p.frames.SetLower(b)
		} else {
			slog.Warn("lower camera capture failed", "err", err)
		}
	}
}

// Run publishes sensors immediately then every interval.
func (p *Publisher) Run() {
	p.publishOnce()
	if p.interval <= 0 {
		slog.Warn("invalid sensor interval, defaulting to 30m", "interval", p.interval)
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
		slog.Warn("invalid camera interval, defaulting to 1h", "interval", interval)
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
