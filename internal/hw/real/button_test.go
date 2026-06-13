package real

import (
	"testing"
	"time"

	"github.com/iot-root/garden-of-eden/internal/hw"
)

func collect(presses []time.Duration, window time.Duration) []hw.ButtonEvent {
	d := newPressDetector(window)
	var out []hw.ButtonEvent
	var t time.Time
	for _, gap := range presses {
		t = t.Add(gap)
		if ev, ok := d.press(t); ok {
			out = append(out, ev)
		}
	}
	if ev, ok := d.flush(t.Add(window + time.Millisecond)); ok {
		out = append(out, ev)
	}
	return out
}

func TestSinglePress(t *testing.T) {
	got := collect([]time.Duration{0}, 300*time.Millisecond)
	if len(got) != 1 || got[0] != hw.SinglePress {
		t.Errorf("got %v, want [SinglePress]", got)
	}
}

func TestDoublePress(t *testing.T) {
	got := collect([]time.Duration{0, 100 * time.Millisecond}, 300*time.Millisecond)
	if len(got) != 1 || got[0] != hw.DoublePress {
		t.Errorf("got %v, want [DoublePress]", got)
	}
}

func TestTwoSinglesWhenFarApart(t *testing.T) {
	got := collect([]time.Duration{0, 500 * time.Millisecond}, 300*time.Millisecond)
	if len(got) != 2 || got[0] != hw.SinglePress || got[1] != hw.SinglePress {
		t.Errorf("got %v, want [SinglePress SinglePress]", got)
	}
}
