package real

import (
	"fmt"
	"sync"
	"time"

	"github.com/iot-root/garden-of-eden/internal/hw"
	"github.com/warthog618/go-gpiocdev"
)

// PumpPWM implements hw.Pump via a software-PWM goroutine at the given freq.
type PumpPWM struct {
	line   *gpiocdev.Line
	period time.Duration
	mu     sync.Mutex
	pct    int
	done   chan struct{}
}

func NewPumpPWM(chip string, gpio, freqHz int) (*PumpPWM, error) {
	line, err := gpiocdev.RequestLine(chip, gpio, gpiocdev.AsOutput(0))
	if err != nil {
		return nil, fmt.Errorf("request gpio%d: %w", gpio, err)
	}
	p := &PumpPWM{
		line:   line,
		period: time.Second / time.Duration(freqHz),
		done:   make(chan struct{}),
	}
	go p.loop()
	return p, nil
}

func (p *PumpPWM) loop() {
	for {
		select {
		case <-p.done:
			return
		default:
		}
		p.mu.Lock()
		pct := p.pct
		p.mu.Unlock()

		switch {
		case pct <= 0:
			_ = p.line.SetValue(0)
			time.Sleep(p.period)
		case pct >= 100:
			_ = p.line.SetValue(1)
			time.Sleep(p.period)
		default:
			high := p.period * time.Duration(pct) / 100
			_ = p.line.SetValue(1)
			time.Sleep(high)
			_ = p.line.SetValue(0)
			time.Sleep(p.period - high)
		}
	}
}

func (p *PumpPWM) SetSpeed(pct int) error {
	if pct < 0 || pct > 100 {
		return fmt.Errorf("speed %d out of range 0..100", pct)
	}
	p.mu.Lock()
	p.pct = pct
	p.mu.Unlock()
	return nil
}

func (p *PumpPWM) Speed() int { p.mu.Lock(); defer p.mu.Unlock(); return p.pct }
func (p *PumpPWM) Off() error { return p.SetSpeed(0) }

func (p *PumpPWM) Close() error {
	close(p.done)
	return p.line.Close()
}

var _ hw.Pump = (*PumpPWM)(nil)
