// Package events provides a thread-safe fixed-capacity ring buffer for
// structured diagnostic events. It is used by core to record pump and
// over-temp lifecycle events, and served by GET /events.
package events

import (
	"sync"
	"time"
)

// Event is a single diagnostic event with a timestamp, a short kind tag, and
// an optional detail string.
type Event struct {
	Time   time.Time `json:"time"`
	Kind   string    `json:"kind"`
	Detail string    `json:"detail,omitempty"`
}

// Recorder is a thread-safe ring buffer with a fixed capacity. When the buffer
// is full the oldest entry is overwritten.
type Recorder struct {
	mu   sync.Mutex
	buf  []Event
	cap  int
	head int // index of the next write slot
	full bool
}

// NewRecorder returns a Recorder with the given capacity (minimum 1).
func NewRecorder(capacity int) *Recorder {
	if capacity < 1 {
		capacity = 1
	}
	return &Recorder{buf: make([]Event, capacity), cap: capacity}
}

// Record appends a new event. If the buffer is full the oldest entry is
// silently overwritten. Safe to call on a nil *Recorder (no-op).
func (r *Recorder) Record(kind, detail string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.buf[r.head] = Event{Time: time.Now(), Kind: kind, Detail: detail}
	r.head = (r.head + 1) % r.cap
	if r.head == 0 {
		r.full = true
	}
	r.mu.Unlock()
}

// Snapshot returns a copy of all recorded events ordered oldest-to-newest.
// Returns nil when called on a nil *Recorder.
func (r *Recorder) Snapshot() []Event {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	var out []Event
	if r.full {
		// oldest entry is at r.head
		out = make([]Event, r.cap)
		copy(out, r.buf[r.head:])
		copy(out[r.cap-r.head:], r.buf[:r.head])
	} else {
		// buffer not yet full: valid entries are buf[0..head)
		out = make([]Event, r.head)
		copy(out, r.buf[:r.head])
	}
	return out
}
