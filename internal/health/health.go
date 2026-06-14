// Package health tracks the last successful read time for each named sensor,
// used by GET /healthz to report per-sensor staleness.
package health

import (
	"sync"
	"time"
)

// StalenessWindow is the maximum age (seconds) before a sensor is considered
// unhealthy. Sensors not read within this window have OK=false in /healthz.
const StalenessWindow = 120 * time.Second

// SensorStatus is the per-sensor entry in the /healthz response.
type SensorStatus struct {
	OK   bool    `json:"ok"`
	AgeS float64 `json:"age_s"`
}

// Tracker records the last successful read time per named sensor.
type Tracker struct {
	mu   sync.RWMutex
	last map[string]time.Time
}

// NewTracker returns an empty Tracker.
func NewTracker() *Tracker {
	return &Tracker{last: make(map[string]time.Time)}
}

// Touch records a successful read of the named sensor at time.Now().
func (t *Tracker) Touch(name string) {
	t.TouchAt(name, time.Now())
}

// TouchAt records a successful read of the named sensor at the given time.
// Used in tests to inject a specific timestamp.
func (t *Tracker) TouchAt(name string, at time.Time) {
	t.mu.Lock()
	t.last[name] = at
	t.mu.Unlock()
}

// SensorAge returns the age in seconds since the last successful read of the
// named sensor, relative to now. ok is false if the sensor was never read.
func (t *Tracker) SensorAge(name string, now time.Time) (age float64, ok bool) {
	t.mu.RLock()
	ts, found := t.last[name]
	t.mu.RUnlock()
	if !found {
		return 0, false
	}
	return now.Sub(ts).Seconds(), true
}

// Snapshot returns a copy of the health status for every sensor that has ever
// been touched, evaluated at now.
func (t *Tracker) Snapshot(now time.Time) map[string]SensorStatus {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make(map[string]SensorStatus, len(t.last))
	for name, ts := range t.last {
		age := now.Sub(ts).Seconds()
		out[name] = SensorStatus{
			OK:   now.Sub(ts) <= StalenessWindow,
			AgeS: age,
		}
	}
	return out
}
