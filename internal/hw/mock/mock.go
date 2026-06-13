// Package mock provides in-memory fakes of the hw interfaces for tests and
// for running gardynd on a laptop via --hw=mock.
package mock

import (
	"fmt"
	"sync"

	"github.com/iot-root/garden-of-eden/internal/hw"
)

type Light struct {
	mu  sync.Mutex
	pct int
}

func (l *Light) SetBrightness(pct int) error {
	if pct < 0 || pct > 100 {
		return fmt.Errorf("brightness %d out of range 0..100", pct)
	}
	l.mu.Lock()
	l.pct = pct
	l.mu.Unlock()
	return nil
}

func (l *Light) Brightness() int { l.mu.Lock(); defer l.mu.Unlock(); return l.pct }
func (l *Light) Off() error      { return l.SetBrightness(0) }

type Pump struct {
	mu  sync.Mutex
	pct int
}

func (p *Pump) SetSpeed(pct int) error {
	if pct < 0 || pct > 100 {
		return fmt.Errorf("speed %d out of range 0..100", pct)
	}
	p.mu.Lock()
	p.pct = pct
	p.mu.Unlock()
	return nil
}

func (p *Pump) Speed() int { p.mu.Lock(); defer p.mu.Unlock(); return p.pct }
func (p *Pump) Off() error { return p.SetSpeed(0) }

type Distance struct{ CM float64 }

func (d *Distance) MeasureCM() (float64, error) {
	if d.CM == 0 {
		return 7.5, nil
	}
	return d.CM, nil
}

type Env struct{ T, H float64 }

func (e *Env) Read() (float64, float64, error) {
	t, h := e.T, e.H
	if t == 0 {
		t = 22.5
	}
	if h == 0 {
		h = 55.0
	}
	return t, h, nil
}

type PCBTemp struct {
	T    float64
	Over bool
}

func (p *PCBTemp) Temperature() (float64, error) {
	if p.T == 0 {
		return 30.0, nil
	}
	return p.T, nil
}
func (p *PCBTemp) OverTemp() (bool, error) { return p.Over, nil }

type Power struct{}

func (Power) Read() (hw.PowerReading, error) {
	return hw.PowerReading{BusVoltage: 12.0, ShuntVoltage: 0.01, Current: 0.5, Power: 6.0}, nil
}

type Camera struct{}

// 1x1 JPEG-ish stub; tests only check non-empty.
func (Camera) Capture() ([]byte, error) { return []byte{0xFF, 0xD8, 0xFF, 0xD9}, nil }

type Button struct{ ch chan hw.ButtonEvent }

func (b *Button) Events() <-chan hw.ButtonEvent { return b.ch }

// New returns a Devices bundle backed by mocks.
func New() hw.Devices {
	return hw.Devices{
		Light:       &Light{},
		Pump:        &Pump{},
		Distance:    &Distance{},
		Env:         &Env{},
		PCBTemp:     &PCBTemp{},
		Power:       Power{},
		UpperCamera: Camera{},
		LowerCamera: Camera{},
		Button:      &Button{ch: make(chan hw.ButtonEvent)},
	}
}
