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

// New returns a Devices bundle backed by mocks.
func New() hw.Devices {
	return hw.Devices{Light: &Light{}, Pump: &Pump{}}
}
