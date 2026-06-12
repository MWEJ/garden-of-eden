package real

import (
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/iot-root/garden-of-eden/internal/hw"
	"github.com/warthog618/go-gpiocdev"
)

const speedOfSoundCmPerS = 34300.0 // ~343 m/s

// HCSR04 implements hw.DistanceSensor.
type HCSR04 struct {
	mu      sync.Mutex
	chip    string
	trigger int
	echo    int
}

func NewHCSR04(chip string, trigger, echo int) *HCSR04 {
	return &HCSR04{chip: chip, trigger: trigger, echo: echo}
}

// MeasureCM pings 10× and returns the median distance in cm. It holds an
// internal lock for the whole sweep; worst case (all samples time out) is
// ~1.9s of blocking, so latency-sensitive callers should invoke it from a
// goroutine with an external deadline.
func (h *HCSR04) MeasureCM() (float64, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	var samples []float64
	for i := 0; i < 10; i++ {
		if cm, err := h.measureOnce(); err == nil {
			samples = append(samples, cm)
		}
		time.Sleep(60 * time.Millisecond) // HC-SR04 datasheet: >=60ms between pings so the prior echo decays
	}
	return medianOrErr(samples)
}

func (h *HCSR04) measureOnce() (float64, error) {
	trig, err := gpiocdev.RequestLine(h.chip, h.trigger, gpiocdev.AsOutput(0))
	if err != nil {
		return 0, err
	}
	defer trig.Close()

	rise := make(chan time.Time, 1)
	fall := make(chan time.Time, 1)
	echo, err := gpiocdev.RequestLine(h.chip, h.echo,
		gpiocdev.WithBothEdges,
		gpiocdev.WithEventHandler(func(ev gpiocdev.LineEvent) {
			ts := time.Now() // TODO(on-Pi): ev.Timestamp (kernel monotonic) is more precise
			if ev.Type == gpiocdev.LineEventRisingEdge {
				select {
				case rise <- ts:
				default:
				}
			} else {
				select {
				case fall <- ts:
				default:
				}
			}
		}))
	if err != nil {
		return 0, err
	}
	defer echo.Close()

	// Drain any spurious edge events captured before the trigger pulse, so a
	// stale rising edge can't be mistaken for t0.
	for len(rise) > 0 {
		<-rise
	}
	for len(fall) > 0 {
		<-fall
	}

	// 10µs trigger pulse.
	_ = trig.SetValue(1)
	time.Sleep(10 * time.Microsecond)
	_ = trig.SetValue(0)

	var t0 time.Time
	select {
	case t0 = <-rise:
	case <-time.After(60 * time.Millisecond):
		return 0, fmt.Errorf("echo rise timeout")
	}
	select {
	case t1 := <-fall:
		width := t1.Sub(t0).Seconds()
		cm := width * speedOfSoundCmPerS / 2
		if cm <= 0 || cm > 400 {
			return 0, fmt.Errorf("implausible distance %.1fcm", cm)
		}
		return cm, nil
	case <-time.After(60 * time.Millisecond):
		return 0, fmt.Errorf("echo fall timeout")
	}
}

func median(data []float64) float64 {
	s := append([]float64(nil), data...)
	sort.Float64s(s)
	n := len(s)
	if n%2 == 1 {
		return s[n/2]
	}
	return (s[n/2-1] + s[n/2]) / 2
}

func medianOrErr(data []float64) (float64, error) {
	if len(data) == 0 {
		return 0, fmt.Errorf("no successful measurements")
	}
	return round2(median(data)), nil
}

func round2(f float64) float64 { return float64(int(f*100+0.5)) / 100 }

var _ hw.DistanceSensor = (*HCSR04)(nil)
