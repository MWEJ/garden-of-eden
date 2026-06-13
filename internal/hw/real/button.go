package real

import (
	"sync"
	"time"

	"github.com/iot-root/garden-of-eden/internal/hw"
	"github.com/warthog618/go-gpiocdev"
)

// pressDetector turns press timestamps into single/double-press gestures.
// It is safe for concurrent use: the GPIO event handler and the flusher
// goroutine both call into it.
type pressDetector struct {
	mu      sync.Mutex
	window  time.Duration
	pending bool
	first   time.Time
}

func newPressDetector(window time.Duration) *pressDetector {
	return &pressDetector{window: window}
}

// press records a press at time t. If it completes a double-press, it returns
// (DoublePress, true) immediately. Otherwise it buffers and returns ok=false;
// call flush after the window to emit a buffered single-press.
func (d *pressDetector) press(t time.Time) (hw.ButtonEvent, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.pending && t.Sub(d.first) <= d.window {
		d.pending = false
		return hw.DoublePress, true
	}
	// If a previous press is pending but expired, emit it as single first.
	var emit bool
	var ev hw.ButtonEvent
	if d.pending {
		ev, emit = hw.SinglePress, true
	}
	d.pending = true
	d.first = t
	if emit {
		return ev, true
	}
	return 0, false
}

// flush emits a buffered single-press if its window has elapsed by time t.
func (d *pressDetector) flush(t time.Time) (hw.ButtonEvent, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.pending && t.Sub(d.first) > d.window {
		d.pending = false
		return hw.SinglePress, true
	}
	return 0, false
}

// GPIOButton implements hw.Button on a falling-edge button with debounce.
type GPIOButton struct {
	ch   chan hw.ButtonEvent
	line *gpiocdev.Line
	det  *pressDetector
	done chan struct{}
}

func NewGPIOButton(chip string, gpio int, window, debounce time.Duration) (*GPIOButton, error) {
	b := &GPIOButton{
		ch:   make(chan hw.ButtonEvent, 4),
		det:  newPressDetector(window),
		done: make(chan struct{}),
	}
	line, err := gpiocdev.RequestLine(chip, gpio,
		gpiocdev.WithPullUp,
		gpiocdev.WithFallingEdge,
		gpiocdev.WithDebounce(debounce),
		gpiocdev.WithEventHandler(func(gpiocdev.LineEvent) {
			if ev, ok := b.det.press(time.Now()); ok {
				b.emit(ev)
			}
		}))
	if err != nil {
		return nil, err
	}
	b.line = line
	go b.flusher(window)
	return b, nil
}

func (b *GPIOButton) flusher(window time.Duration) {
	ticker := time.NewTicker(window)
	defer ticker.Stop()
	for {
		select {
		case <-b.done:
			return
		case now := <-ticker.C:
			if ev, ok := b.det.flush(now); ok {
				b.emit(ev)
			}
		}
	}
}

func (b *GPIOButton) emit(ev hw.ButtonEvent) {
	select {
	case b.ch <- ev:
	default:
	}
}

func (b *GPIOButton) Events() <-chan hw.ButtonEvent { return b.ch }

// Close stops the flusher goroutine and releases the GPIO line.
func (b *GPIOButton) Close() error {
	close(b.done)
	return b.line.Close()
}

var _ hw.Button = (*GPIOButton)(nil)
