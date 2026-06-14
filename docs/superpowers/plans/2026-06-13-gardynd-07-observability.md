# gardynd Plan 7 — Observability Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add structured logging (slog), an event ring buffer with `GET /events`, a hand-rolled Prometheus-compatible `GET /metrics` endpoint, and richer per-sensor health tracking in `GET /healthz` — all without adding any new dependencies.

**Architecture:** Four orthogonal capabilities. A new `internal/events` package (ring buffer, no deps) is threaded into core via a `SetEvents` setter; httpapi grows two new routes (`/events`, `/metrics`). A new `internal/health` package tracks per-sensor last-read timestamps; the publisher updates it; httpapi reads it for the enriched `/healthz`. Structured logging is a mechanical substitution of `log.Printf` with `slog` calls in the five files that use it, controlled by a `LogLevel` field added to `Config`. Main wires everything together in the final task. Each feature touches a disjoint set of packages, enabling Tasks 1–5 (config/logging, events, core-events, health tracker, metrics) to be developed independently once their leaf packages exist.

**Tech Stack:** Go stdlib only — `log/slog`, `sync/atomic`, `time`, `net/http`; no new module dependencies. Handwritten Prometheus text exposition (`text/plain; version=0.0.4`) for `GET /metrics`.

**Spec:** `docs/superpowers/specs/2026-06-12-gardynd-go-service-design.md`
**Depends on:** Plan 1 (core, state store, httpapi, config), Plan 2 (sensor interfaces, `sensorMux`), Plan 3 (publisher, scheduler, `HandlerFull`, `ControlDeps`), Plan 6 (state freshness, `publish.New` 5-arg signature).

---

## File Structure (this plan)

```
internal/config/config.go          MODIFY: add LogLevel string field, applyEnv hook, default "info"
internal/config/config_test.go     MODIFY: append LogLevel parse tests
internal/events/events.go          CREATE: thread-safe ring Recorder + Event type
internal/events/events_test.go     CREATE: unit tests for Recorder
internal/core/core.go              MODIFY: add rec *events.Recorder field; add SetEvents setter; call rec.Record in applyPump/applyOverTemp
internal/core/core_test.go         MODIFY: append interlock-records-event test
internal/health/health.go          CREATE: Tracker — per-sensor last-read timestamps
internal/health/health_test.go     CREATE: unit tests for Tracker
internal/publish/publish.go        MODIFY: accept *health.Tracker param; call tracker.Touch in publishOnce
internal/publish/publish_test.go   MODIFY: append test that Touch is called on successful read
internal/httpapi/httpapi.go        MODIFY: extend HandlerFull to accept *events.Recorder + *health.Tracker; add GET /events, GET /metrics, richer GET /healthz
internal/httpapi/httpapi_test.go   MODIFY: append tests for GET /events, GET /metrics, GET /healthz health fields
cmd/gardynd/main.go                MODIFY: configure slog default; wire events.Recorder + health.Tracker; replace log.Printf → slog calls
```

---

### Task 1: Config — `LogLevel` field + slog default in main

**Depends on:** none (within this plan; touches only `internal/config/` and then `cmd/gardynd/main.go` in the final wiring task)

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`

Add `LogLevel string` (yaml `log_level`, env `LOG_LEVEL`, default `"info"`) to `Config`. The field is parsed and validated; main uses it to set the `slog` default handler level. No go test can directly assert slog output (output goes to stderr and the handler is a global), so the TDD focus is on the config parsing and level conversion function.

- [ ] **Step 1: Write the failing tests (append to config_test.go)**

Append to `internal/config/config_test.go`:
```go
func TestLogLevelDefault(t *testing.T) {
	c, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if c.LogLevel != "info" {
		t.Errorf("LogLevel default = %q, want %q", c.LogLevel, "info")
	}
}

func TestLogLevelEnvOverride(t *testing.T) {
	t.Setenv("LOG_LEVEL", "debug")
	c, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if c.LogLevel != "debug" {
		t.Errorf("LogLevel = %q, want %q", c.LogLevel, "debug")
	}
}

func TestParseLogLevel(t *testing.T) {
	cases := []struct {
		in   string
		want string // slog level name from level.String()
	}{
		{"debug", "DEBUG"},
		{"info", "INFO"},
		{"warn", "WARN"},
		{"error", "ERROR"},
		{"", "INFO"},     // empty → default
		{"bogus", "INFO"}, // unknown → default
	}
	for _, tc := range cases {
		lv := ParseLogLevel(tc.in)
		if lv.String() != tc.want {
			t.Errorf("ParseLogLevel(%q).String() = %q, want %q", tc.in, lv.String(), tc.want)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config/ -run 'TestLogLevel|TestParseLogLevel' -v`
Expected: build failure — `c.LogLevel undefined` and `config.ParseLogLevel undefined`.

- [ ] **Step 3: Add LogLevel field, default, env hook, and ParseLogLevel to config.go**

In `internal/config/config.go`, add `LogLevel` to `Config`:
```go
type Config struct {
	HTTP       HTTPConfig     `yaml:"http"`
	Device     DeviceConfig   `yaml:"device"`
	Camera     CameraConfig   `yaml:"camera"`
	SensorType string         `yaml:"sensor_type"`
	Schedules  Schedules      `yaml:"schedules"`
	Water      WaterConfig    `yaml:"water"`
	OverTemp   OverTempConfig `yaml:"overtemp"`
	LogLevel   string         `yaml:"log_level"`
}
```

In `defaults()`, set the new default:
```go
func defaults() Config {
	return Config{
		HTTP:   HTTPConfig{Port: 5000},
		Device: DeviceConfig{Identifier: "gardyn-xx", Model: "gardyn 3.0", Version: "1.0.0"},
		Camera: CameraConfig{
			UpperDevice:     "/dev/video0",
			LowerDevice:     "/dev/video2",
			Resolution:      "640x480",
			IntervalSeconds: 3600,
		},
		SensorType: "AM2320",
		LogLevel:   "info",
	}
}
```

In `applyEnv`, add the env hook after the existing `envStr` calls:
```go
func applyEnv(c *Config) {
	envInt(&c.HTTP.Port, "HTTP_PORT")
	envStr(&c.Device.Identifier, "MQTT_IDENTIFIER")
	envStr(&c.Device.Model, "MQTT_DEVICE_MODEL")
	envStr(&c.Device.Version, "MQTT_VERSION")
	envStr(&c.Camera.UpperDevice, "UPPER_CAMERA_DEVICE")
	envStr(&c.Camera.LowerDevice, "LOWER_CAMERA_DEVICE")
	envStr(&c.Camera.Resolution, "CAMERA_RESOLUTION")
	envInt(&c.Camera.IntervalSeconds, "IMAGE_INTERVAL_SECONDS")
	envStr(&c.SensorType, "SENSOR_TYPE")
	envFloat(&c.Water.LowCM, "WATER_LOW_CM")
	envStr(&c.LogLevel, "LOG_LEVEL")
}
```

Add `ParseLogLevel` at the bottom of `config.go` — add `"log/slog"` to the import block:
```go
import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"

	"gopkg.in/yaml.v3"
)
```

```go
// ParseLogLevel converts a string level name ("debug", "info", "warn", "error",
// case-insensitive) to a slog.Level. Unknown or empty strings default to
// slog.LevelInfo.
func ParseLogLevel(s string) slog.Level {
	switch s {
	case "debug", "DEBUG":
		return slog.LevelDebug
	case "info", "INFO":
		return slog.LevelInfo
	case "warn", "WARN", "warning", "WARNING":
		return slog.LevelWarn
	case "error", "ERROR":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/config/ -run 'TestLogLevel|TestParseLogLevel' -v`
Expected:
```
--- PASS: TestLogLevelDefault (0.00s)
--- PASS: TestLogLevelEnvOverride (0.00s)
--- PASS: TestParseLogLevel (0.00s)
PASS
```

- [ ] **Step 5: Run full config suite to confirm no regressions**

Run: `go test ./internal/config/ -v`
Expected: all existing tests PASS plus the three new ones.

---

### Task 2: `internal/events` — thread-safe ring Recorder

**Depends on:** none (new package, no peer-task interfaces required)

**Files:**
- Create: `internal/events/events.go`
- Create: `internal/events/events_test.go`

The `Recorder` is a fixed-capacity ring buffer (capacity 100). `Record` stamps `time.Now()`. `Snapshot` returns a copy oldest-to-newest; the copy is safe to marshal without holding the lock.

- [ ] **Step 1: Write the failing tests**

Create `internal/events/events_test.go`:
```go
package events_test

import (
	"testing"
	"time"

	"github.com/iot-root/garden-of-eden/internal/events"
)

func TestRecordAndSnapshot(t *testing.T) {
	r := events.NewRecorder(10)
	r.Record("pump_on", "speed=80")
	r.Record("pump_off", "")

	snap := r.Snapshot()
	if len(snap) != 2 {
		t.Fatalf("len(snap) = %d, want 2", len(snap))
	}
	if snap[0].Kind != "pump_on" {
		t.Errorf("snap[0].Kind = %q, want %q", snap[0].Kind, "pump_on")
	}
	if snap[1].Kind != "pump_off" {
		t.Errorf("snap[1].Kind = %q, want %q", snap[1].Kind, "pump_off")
	}
	if snap[0].Time.IsZero() {
		t.Error("snap[0].Time is zero, want time.Now()-ish")
	}
}

func TestRingOverflow(t *testing.T) {
	r := events.NewRecorder(3)
	r.Record("a", "1")
	r.Record("b", "2")
	r.Record("c", "3")
	r.Record("d", "4") // overwrites "a"

	snap := r.Snapshot()
	if len(snap) != 3 {
		t.Fatalf("len(snap) = %d, want 3", len(snap))
	}
	// oldest-to-newest: b, c, d
	if snap[0].Kind != "b" || snap[1].Kind != "c" || snap[2].Kind != "d" {
		t.Errorf("snap kinds = [%q,%q,%q], want [b,c,d]", snap[0].Kind, snap[1].Kind, snap[2].Kind)
	}
}

func TestSnapshotOldestFirst(t *testing.T) {
	r := events.NewRecorder(5)
	for _, k := range []string{"x", "y", "z"} {
		r.Record(k, "")
		time.Sleep(time.Millisecond) // ensure ordering is observable via insertion order, not time
	}
	snap := r.Snapshot()
	if snap[0].Kind != "x" || snap[1].Kind != "y" || snap[2].Kind != "z" {
		t.Errorf("snap kinds = [%q,%q,%q], want [x,y,z]", snap[0].Kind, snap[1].Kind, snap[2].Kind)
	}
}

func TestSnapshotOnNilRecorder(t *testing.T) {
	// A nil *Recorder must not panic — core may call Record before SetEvents.
	var r *events.Recorder
	r.Record("anything", "") // must not panic
	snap := r.Snapshot()
	if snap != nil {
		t.Errorf("nil recorder Snapshot = %v, want nil", snap)
	}
}

func TestSnapshotIsACopy(t *testing.T) {
	r := events.NewRecorder(5)
	r.Record("first", "")
	snap := r.Snapshot()
	// Mutating the returned slice must not affect the ring.
	snap[0].Kind = "mutated"
	snap2 := r.Snapshot()
	if snap2[0].Kind != "first" {
		t.Errorf("ring was mutated by caller modifying snapshot: got %q", snap2[0].Kind)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/events/ -v`
Expected: build failure — `package events` does not exist.

- [ ] **Step 3: Implement the Recorder**

Create `internal/events/events.go`:
```go
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
	if !r.full && r.head == 0 {
		// capacity==1 edge case: head wraps immediately after first write
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
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/events/ -v`
Expected:
```
--- PASS: TestRecordAndSnapshot (0.00s)
--- PASS: TestRingOverflow (0.00s)
--- PASS: TestSnapshotOldestFirst (0.00s)
--- PASS: TestSnapshotOnNilRecorder (0.00s)
--- PASS: TestSnapshotIsACopy (0.00s)
PASS
```

- [ ] **Step 5: Run with race detector**

Run: `go test ./internal/events/ -race -v`
Expected: PASS, no races.

---

### Task 3: Core — `SetEvents` setter + `Record` calls

**Depends on:** Task 2 (events package must exist and compile)

**Files:**
- Modify: `internal/core/core.go`
- Modify: `internal/core/core_test.go`

Add a `rec *events.Recorder` field to `Core`. Provide a `SetEvents(*events.Recorder)` setter (called from main after `core.New`). In `applyPump` record `pump_on`/`pump_off`/`interlock_block`. In `applyOverTemp` record `overtemp`/`overtemp_clear`. In `armPumpFailsafe` record `pump_failsafe`. All calls are guarded via the nil-safe `rec.Record` no-op.

- [ ] **Step 1: Write the failing test (append to core_test.go)**

Append to `internal/core/core_test.go`:
```go
func TestInterlockBlockRecordsEvent(t *testing.T) {
	st := state.New()
	devs := mock.New()
	devs.Distance.(*mock.Distance).CM = 12.0 // > threshold => water low => interlock fires
	c := New(devs, st)
	c.SetWaterLowCM(10.0)

	rec := events.NewRecorder(20)
	c.SetEvents(rec)

	go c.Run()
	defer c.Stop()

	c.Submit(Command{Target: TargetPump, Action: ActionOn})

	// Wait for water.low to be set (interlock has fired).
	for i := 0; i < 50; i++ {
		if st.Snapshot().Water.Low {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !st.Snapshot().Water.Low {
		t.Fatal("interlock did not fire (water.low not set)")
	}

	snap := rec.Snapshot()
	found := false
	for _, ev := range snap {
		if ev.Kind == "interlock_block" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("interlock_block event not recorded; events = %+v", snap)
	}
}

func TestPumpOnOffRecordsEvents(t *testing.T) {
	st := state.New()
	c := New(mock.New(), st)
	rec := events.NewRecorder(20)
	c.SetEvents(rec)
	go c.Run()
	defer c.Stop()

	c.Submit(Command{Target: TargetPump, Action: ActionOn})
	for i := 0; i < 50; i++ {
		if st.Snapshot().Pump.On {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	c.Submit(Command{Target: TargetPump, Action: ActionOff})
	for i := 0; i < 50; i++ {
		if !st.Snapshot().Pump.On {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	snap := rec.Snapshot()
	kinds := make(map[string]bool)
	for _, ev := range snap {
		kinds[ev.Kind] = true
	}
	if !kinds["pump_on"] {
		t.Errorf("pump_on event not recorded; events = %+v", snap)
	}
	if !kinds["pump_off"] {
		t.Errorf("pump_off event not recorded; events = %+v", snap)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/core/ -run 'TestInterlockBlockRecordsEvent|TestPumpOnOffRecordsEvents' -v`
Expected: build failure — `events undefined` and `c.SetEvents undefined`.

- [ ] **Step 3: Implement SetEvents and Record calls in core.go**

Add `"github.com/iot-root/garden-of-eden/internal/events"` to the imports in `internal/core/core.go`.

Add the `rec` field to the `Core` struct:
```go
type Core struct {
	dev        hw.Devices
	store      *state.Store
	cmds       chan Command
	done       chan struct{}
	stopOnce   sync.Once
	lightLevel int
	pumpLevel  int

	cfgMu              sync.Mutex
	waterLowCM         float64
	pumpMaxRuntime     time.Duration
	cutLightOnOverTemp bool
	pumpTimer          *time.Timer

	rec *events.Recorder // guarded by nil-safe Recorder.Record; set via SetEvents
}
```

Add the setter after `New`:
```go
// SetEvents wires an event recorder into the core. Must be called before
// Run (no locking needed — Run and Submit are not yet in flight).
func (c *Core) SetEvents(rec *events.Recorder) { c.rec = rec }
```

Update `applyPump` to record events. Replace the existing `applyPump` implementation:
```go
func (c *Core) applyPump(cmd Command) {
	switch cmd.Action {
	case ActionOn:
		threshold := c.waterLow()
		if threshold > 0 {
			cm, err := c.measureDistance()
			if err == nil && cm > threshold {
				c.store.SetWater(threshold, true) // low=true
				c.rec.Record("interlock_block", fmt.Sprintf("distance=%.1fcm threshold=%.1fcm", cm, threshold))
				c.flashLights()
				return
			}
			c.store.SetWater(threshold, false)
		}
		if cmd.Value > 0 {
			c.pumpLevel = cmd.Value
		}
		if err := c.dev.Pump.SetSpeed(c.pumpLevel); err != nil {
			log.Printf("pump on: %v", err)
			return
		}
		c.armPumpFailsafe()
		c.store.SetPump(true, c.pumpLevel)
		c.rec.Record("pump_on", fmt.Sprintf("speed=%d", c.pumpLevel))
	case ActionOff:
		c.disarmPumpFailsafe()
		if err := c.dev.Pump.Off(); err != nil {
			log.Printf("pump off: %v", err)
			return
		}
		c.store.SetPump(false, c.pumpLevel)
		c.rec.Record("pump_off", "")
	case ActionSetLevel:
		c.pumpLevel = cmd.Value
		if err := c.dev.Pump.SetSpeed(cmd.Value); err != nil {
			log.Printf("pump level: %v", err)
			return
		}
		c.store.SetPump(cmd.Value > 0, cmd.Value)
	}
}
```

Update `applyOverTemp` to record events:
```go
func (c *Core) applyOverTemp(cmd Command) {
	alert := cmd.Value == 1
	c.store.SetOverTemp(alert)
	if alert {
		c.rec.Record("overtemp", "")
		if c.cutLightOnTemp() && c.dev.Light != nil {
			_ = c.dev.Light.Off()
			c.store.SetLight(false, c.lightLevel)
		}
	} else {
		c.rec.Record("overtemp_clear", "")
	}
}
```

Update `armPumpFailsafe` to record the event:
```go
func (c *Core) armPumpFailsafe() {
	maxRT := c.pumpMaxRT()
	if maxRT <= 0 {
		return
	}
	c.disarmPumpFailsafe()
	c.pumpTimer = time.AfterFunc(maxRT, func() {
		c.rec.Record("pump_failsafe", fmt.Sprintf("after=%s", maxRT))
		c.Submit(Command{Target: TargetPump, Action: ActionOff})
		log.Printf("pump failsafe: forced off after %s", maxRT)
	})
}
```

- [ ] **Step 4: Run new tests to verify they pass**

Run: `go test ./internal/core/ -run 'TestInterlockBlockRecordsEvent|TestPumpOnOffRecordsEvents' -v`
Expected:
```
--- PASS: TestInterlockBlockRecordsEvent (0.00s)
--- PASS: TestPumpOnOffRecordsEvents (0.00s)
PASS
```

- [ ] **Step 5: Run full core suite to confirm no regressions**

Run: `go test ./internal/core/ -race -v`
Expected: all existing tests PASS plus the two new ones, no races.

---

### Task 4: `internal/health` — per-sensor last-read Tracker

**Depends on:** none (new package, no peer-task interfaces required)

**Files:**
- Create: `internal/health/health.go`
- Create: `internal/health/health_test.go`

The `Tracker` records the last successful read time per named sensor. `Touch(name string)` stamps `time.Now()`. `SensorAge(name string, now time.Time) (age float64, ok bool)` returns the age in seconds since the last touch, `ok=false` if never touched. `Snapshot(now time.Time) map[string]SensorStatus` returns one entry per sensor that was ever touched. `SensorStatus` is the JSON-friendly struct used by `/healthz`.

- [ ] **Step 1: Write the failing tests**

Create `internal/health/health_test.go`:
```go
package health_test

import (
	"testing"
	"time"

	"github.com/iot-root/garden-of-eden/internal/health"
)

func TestTouchAndAge(t *testing.T) {
	tr := health.NewTracker()
	before := time.Now()
	tr.Touch("env")
	after := time.Now()

	age, ok := tr.SensorAge("env", after)
	if !ok {
		t.Fatal("SensorAge ok = false after Touch")
	}
	if age < 0 || age > after.Sub(before).Seconds()+0.01 {
		t.Errorf("SensorAge = %.4f, expected ~0", age)
	}
}

func TestNeverTouched(t *testing.T) {
	tr := health.NewTracker()
	_, ok := tr.SensorAge("pcb_temp", time.Now())
	if ok {
		t.Error("SensorAge ok = true for sensor that was never touched")
	}
}

func TestSnapshotContainsAllTouched(t *testing.T) {
	tr := health.NewTracker()
	tr.Touch("env")
	tr.Touch("distance")

	snap := tr.Snapshot(time.Now())
	if _, ok := snap["env"]; !ok {
		t.Error("snapshot missing 'env'")
	}
	if _, ok := snap["distance"]; !ok {
		t.Error("snapshot missing 'distance'")
	}
	// A sensor we never touched must not appear.
	if _, ok := snap["pump_power"]; ok {
		t.Error("snapshot has 'pump_power' which was never touched")
	}
}

func TestSnapshotOKWhenFresh(t *testing.T) {
	tr := health.NewTracker()
	tr.Touch("env")
	snap := tr.Snapshot(time.Now())
	if !snap["env"].OK {
		t.Error("env should be OK (just touched)")
	}
}

func TestSnapshotNotOKWhenStale(t *testing.T) {
	tr := health.NewTracker()
	// Fake a read that happened 200 seconds ago.
	past := time.Now().Add(-200 * time.Second)
	tr.TouchAt("env", past)

	snap := tr.Snapshot(time.Now())
	if snap["env"].OK {
		t.Errorf("env should be stale (age=200s > staleness window 120s), got OK=true")
	}
}

func TestConcurrentTouch(t *testing.T) {
	tr := health.NewTracker()
	done := make(chan struct{})
	for i := 0; i < 8; i++ {
		go func() {
			for j := 0; j < 200; j++ {
				tr.Touch("env")
				tr.Snapshot(time.Now())
			}
			done <- struct{}{}
		}()
	}
	for i := 0; i < 8; i++ {
		<-done
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/health/ -v`
Expected: build failure — `package health` does not exist.

- [ ] **Step 3: Implement the Tracker**

Create `internal/health/health.go`:
```go
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
	OK    bool    `json:"ok"`
	AgeS  float64 `json:"age_s"`
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
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/health/ -v`
Expected:
```
--- PASS: TestTouchAndAge (0.00s)
--- PASS: TestNeverTouched (0.00s)
--- PASS: TestSnapshotContainsAllTouched (0.00s)
--- PASS: TestSnapshotOKWhenFresh (0.00s)
--- PASS: TestSnapshotNotOKWhenStale (0.00s)
--- PASS: TestConcurrentTouch (0.00s)
PASS
```

- [ ] **Step 5: Run with race detector**

Run: `go test ./internal/health/ -race -v`
Expected: PASS, no races.

---

### Task 5: Publisher — touch health tracker on successful reads

**Depends on:** Task 4 (health package must exist and compile)

**Files:**
- Modify: `internal/publish/publish.go`
- Modify: `internal/publish/publish_test.go`

Extend `Publisher` to accept an optional `*health.Tracker` (nil-safe). After each successful sensor read in `publishOnce`, call `tracker.Touch(sensorName)` using the canonical names `"env"`, `"pcb_temp"`, `"distance"`, `"pump_power"`. Add `SetHealthTracker(*health.Tracker)` setter so main can wire it in without changing the `New` signature.

- [ ] **Step 1: Write the failing test (append to publish_test.go)**

Append to `internal/publish/publish_test.go`:
```go
func TestPublishOnceTouchesHealthTracker(t *testing.T) {
	devs := mock.New()
	st := state.New()
	p := New(devs, st, state.NewFrames(), 0, nil)

	tr := health.NewTracker()
	p.SetHealthTracker(tr)
	p.publishOnce()

	now := time.Now()
	for _, name := range []string{"env", "pcb_temp", "distance", "pump_power"} {
		_, ok := tr.SensorAge(name, now)
		if !ok {
			t.Errorf("health tracker: sensor %q not touched after publishOnce", name)
		}
	}
}
```

The test file will need the imports `"time"` and `"github.com/iot-root/garden-of-eden/internal/health"` added to its import block. The full import block for `internal/publish/publish_test.go` after this change:
```go
import (
	"testing"
	"time"

	"github.com/iot-root/garden-of-eden/internal/health"
	"github.com/iot-root/garden-of-eden/internal/hw/mock"
	"github.com/iot-root/garden-of-eden/internal/state"
)
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/publish/ -run TestPublishOnceTouchesHealthTracker -v`
Expected: build failure — `p.SetHealthTracker undefined`.

- [ ] **Step 3: Add the tracker field and Touch calls to publish.go**

In `internal/publish/publish.go`, add `"github.com/iot-root/garden-of-eden/internal/health"` to the imports.

Add the `tracker` field to `Publisher`:
```go
type Publisher struct {
	dev        hw.Devices
	store      *state.Store
	frames     *state.Frames
	interval   time.Duration
	onOverTemp func(bool)
	done       chan struct{}
	stopOnce   sync.Once
	tracker    *health.Tracker // nil is safe; Touch is never called on nil
}
```

Add the setter after `New`:
```go
// SetHealthTracker wires a health tracker into the publisher. Must be called
// before Run. nil is accepted (disables health tracking).
func (p *Publisher) SetHealthTracker(tr *health.Tracker) { p.tracker = tr }
```

Update `publishOnce` to call `p.tracker.Touch` after each successful read:
```go
func (p *Publisher) publishOnce() {
	if p.dev.Env != nil {
		if t, h, err := p.dev.Env.Read(); err == nil {
			p.store.SetTemperature(t)
			p.store.SetHumidity(h)
			if p.tracker != nil {
				p.tracker.Touch("env")
			}
		} else {
			log.Printf("env read: %v", err)
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
			if p.tracker != nil {
				p.tracker.Touch("distance")
			}
			snap := p.store.Snapshot()
			if snap.Water.LowThresholdCM > 0 {
				low := cm > snap.Water.LowThresholdCM
				p.store.SetWater(snap.Water.LowThresholdCM, low)
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
```

- [ ] **Step 4: Run new test to verify it passes**

Run: `go test ./internal/publish/ -run TestPublishOnceTouchesHealthTracker -v`
Expected:
```
--- PASS: TestPublishOnceTouchesHealthTracker (0.00s)
PASS
```

- [ ] **Step 5: Run full publish suite to confirm no regressions**

Run: `go test ./internal/publish/ -race -v`
Expected: all existing tests PASS plus the new one, no races.

---

### Task 6: httpapi — `GET /events`, `GET /metrics`, richer `GET /healthz`

**Depends on:** Task 2 (events package), Task 4 (health package). Tasks 2 and 4 are both dependency-free and can be completed in parallel before this task begins.

**Files:**
- Modify: `internal/httpapi/httpapi.go`
- Modify: `internal/httpapi/httpapi_test.go`

Extend `HandlerFull` to accept `*events.Recorder` and `*health.Tracker` as additional parameters. Add three route handlers:

- `GET /events` — returns `rec.Snapshot()` as JSON (empty array if recorder is nil).
- `GET /metrics` — hand-rolled Prometheus text exposition (`Content-Type: text/plain; version=0.0.4`), `gardynd_` prefix, `# HELP` + `# TYPE` per metric. Gauges from the store snapshot; counters derived from scanning the event recorder. Nil sensor pointers are skipped (metric line not emitted).
- `GET /healthz` — now returns `{status, uptime_s, sensors: {<name>: {ok, age_s}}}`. Relies on `*health.Tracker.Snapshot(time.Now())`.

Because `HandlerFull`'s signature changes, its callers in `httpapi_test.go` and `cmd/gardynd/main.go` (Task 8) must be updated.

- [ ] **Step 1: Write the failing tests (append to httpapi_test.go)**

Append to `internal/httpapi/httpapi_test.go`. First, add the new imports to the existing import block:
```go
import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/iot-root/garden-of-eden/internal/config"
	"github.com/iot-root/garden-of-eden/internal/core"
	"github.com/iot-root/garden-of-eden/internal/events"
	"github.com/iot-root/garden-of-eden/internal/health"
	"github.com/iot-root/garden-of-eden/internal/hw"
	"github.com/iot-root/garden-of-eden/internal/hw/mock"
	"github.com/iot-root/garden-of-eden/internal/state"
)
```

Add a helper that creates a full handler with event recorder and health tracker:
```go
func newFullH(t *testing.T) (http.Handler, *events.Recorder, *health.Tracker, func()) {
	t.Helper()
	st := state.New()
	c := core.New(mock.New(), st)
	go c.Run()
	rec := events.NewRecorder(100)
	tr := health.NewTracker()
	deps := ControlDeps{
		GetSchedules:       func() config.Schedules { return config.Schedules{} },
		PutSchedule:        func(string, config.Schedule) error { return nil },
		SetScheduleEnabled: func(string, bool) error { return nil },
		SetWaterLowCM:      func(float64) error { return nil },
	}
	h := HandlerFull(c, st, mock.New(), state.NewFrames(), deps, rec, tr)
	return h, rec, tr, c.Stop
}
```

Add the three new test functions:
```go
func TestGetEvents(t *testing.T) {
	h, rec, _, stop := newFullH(t)
	defer stop()

	rec.Record("pump_on", "speed=100")
	rec.Record("pump_off", "")

	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, httptest.NewRequest(http.MethodGet, "/events", nil))
	if rec2.Code != http.StatusOK {
		t.Fatalf("/events status = %d, want 200", rec2.Code)
	}
	ct := rec2.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	var evs []map[string]any
	if err := json.NewDecoder(rec2.Body).Decode(&evs); err != nil {
		t.Fatalf("decode /events body: %v", err)
	}
	if len(evs) != 2 {
		t.Errorf("len(events) = %d, want 2", len(evs))
	}
	if evs[0]["kind"] != "pump_on" {
		t.Errorf("events[0].kind = %v, want pump_on", evs[0]["kind"])
	}
}

func TestGetMetrics(t *testing.T) {
	h, rec, _, stop := newFullH(t)
	defer stop()

	rec.Record("pump_on", "speed=100")
	rec.Record("interlock_block", "distance=15.0cm threshold=10.0cm")
	rec.Record("pump_failsafe", "after=10m0s")
	rec.Record("overtemp", "")

	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rec2.Code != http.StatusOK {
		t.Fatalf("/metrics status = %d, want 200", rec2.Code)
	}
	ct := rec2.Header().Get("Content-Type")
	if ct != "text/plain; version=0.0.4" {
		t.Errorf("Content-Type = %q, want text/plain; version=0.0.4", ct)
	}
	body := rec2.Body.String()
	for _, substr := range []string{
		"gardynd_temperature_c",
		"gardynd_pump_runs_total",
		"# HELP",
		"# TYPE",
	} {
		if !strings.Contains(body, substr) {
			t.Errorf("/metrics body missing %q\nbody:\n%s", substr, body)
		}
	}
}

func TestGetHealthzRich(t *testing.T) {
	h, _, tr, stop := newFullH(t)
	defer stop()

	tr.Touch("env")
	tr.Touch("distance")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("/healthz status = %d, want 200", rec.Code)
	}
	var body struct {
		Status  string                       `json:"status"`
		UptimeS int64                        `json:"uptime_s"`
		Sensors map[string]health.SensorStatus `json:"sensors"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode /healthz body: %v", err)
	}
	if body.Status != "ok" {
		t.Errorf("status = %q, want ok", body.Status)
	}
	if _, ok := body.Sensors["env"]; !ok {
		t.Error("/healthz sensors missing 'env'")
	}
	if !body.Sensors["env"].OK {
		t.Error("/healthz sensors env.ok = false, want true (just touched)")
	}
	if _, ok := body.Sensors["distance"]; !ok {
		t.Error("/healthz sensors missing 'distance'")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/httpapi/ -run 'TestGetEvents|TestGetMetrics|TestGetHealthzRich' -v`
Expected: build failure — `HandlerFull` called with wrong number of arguments.

- [ ] **Step 3: Extend HandlerFull and add the three routes**

In `internal/httpapi/httpapi.go`, add imports:
```go
import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/iot-root/garden-of-eden/internal/config"
	"github.com/iot-root/garden-of-eden/internal/core"
	"github.com/iot-root/garden-of-eden/internal/events"
	"github.com/iot-root/garden-of-eden/internal/health"
	"github.com/iot-root/garden-of-eden/internal/hw"
	"github.com/iot-root/garden-of-eden/internal/state"
)
```

Update the `HandlerFull` signature to accept the two new parameters:
```go
// HandlerFull is the complete API: base control + sensor reads + schedules +
// water threshold + cameras + events + metrics + rich healthz.
func HandlerFull(c *core.Core, st *state.Store, d hw.Devices, frames *state.Frames, deps ControlDeps, rec *events.Recorder, tr *health.Tracker) http.Handler {
	mux := sensorMux(c, st, d)

	// ... (all existing route registrations from PUT /schedules, POST /schedule/{channel}/enabled,
	// POST /water/low-threshold, GET /camera/*.jpg remain unchanged) ...

	// GET /events — event ring buffer snapshot
	mux.HandleFunc("GET /events", func(w http.ResponseWriter, _ *http.Request) {
		var snap []events.Event
		if rec != nil {
			snap = rec.Snapshot()
		}
		if snap == nil {
			snap = []events.Event{} // encode as [] not null
		}
		writeJSON(w, http.StatusOK, snap)
	})

	// GET /metrics — hand-rolled Prometheus text exposition
	mux.HandleFunc("GET /metrics", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		w.WriteHeader(http.StatusOK)
		writeMetrics(w, st, rec)
	})

	// GET /healthz — richer health check with per-sensor status
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		snap := st.Snapshot()
		var sensors map[string]health.SensorStatus
		if tr != nil {
			sensors = tr.Snapshot(time.Now())
		} else {
			sensors = map[string]health.SensorStatus{}
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"status":   "ok",
			"uptime_s": snap.UptimeS,
			"sensors":  sensors,
		})
	})

	return mux
}
```

Note: the existing `GET /healthz` registration in `baseMux` will be overridden by this registration in `HandlerFull`'s mux (Go 1.22+ method+pattern routing; the more specific `HandlerFull` registration wins because it is registered on the same mux — actually, since `baseMux` registers `GET /healthz` first and then `HandlerFull` tries to register it again on the same mux, this will panic. To fix this, remove the `GET /healthz` registration from `baseMux` and register it only in `HandlerFull`. Update `baseMux` accordingly — remove the `/healthz` line — and add it back in `HandlerFull` alongside the enriched version:

The updated `baseMux` body (remove the healthz registration):
```go
func baseMux(c *core.Core, st *state.Store) *http.ServeMux {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /state", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, st.Snapshot())
	})
	// Note: GET /healthz is registered by HandlerFull (enriched) or callers of
	// baseMux directly (simple). The simple version is added below for Handler().
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	mux.HandleFunc("POST /light/on", func(w http.ResponseWriter, _ *http.Request) {
		c.Submit(core.Command{Target: core.TargetLight, Action: core.ActionOn})
		writeJSON(w, http.StatusOK, map[string]string{"message": "Light turned on"})
	})
	mux.HandleFunc("POST /light/off", func(w http.ResponseWriter, _ *http.Request) {
		c.Submit(core.Command{Target: core.TargetLight, Action: core.ActionOff})
		writeJSON(w, http.StatusOK, map[string]string{"message": "Light turned off"})
	})
	mux.HandleFunc("POST /light/brightness", levelHandler(c, core.TargetLight))

	mux.HandleFunc("POST /pump/on", func(w http.ResponseWriter, _ *http.Request) {
		c.Submit(core.Command{Target: core.TargetPump, Action: core.ActionOn})
		writeJSON(w, http.StatusOK, map[string]string{"message": "Pump turned on!"})
	})
	mux.HandleFunc("POST /pump/off", func(w http.ResponseWriter, _ *http.Request) {
		c.Submit(core.Command{Target: core.TargetPump, Action: core.ActionOff})
		writeJSON(w, http.StatusOK, map[string]string{"message": "Pump turned off!"})
	})
	mux.HandleFunc("POST /pump/speed", levelHandler(c, core.TargetPump))

	return mux
}
```

Because `baseMux` already registers `GET /healthz` and then `HandlerFull` (which builds on `baseMux` via `sensorMux`) would try to register it again, Go's `http.ServeMux` will panic with "pattern already registered". The fix: do not re-register `/healthz` in `HandlerFull`; instead, keep the simple `/healthz` in `baseMux` and replace it in `HandlerFull` by registering the method+path pattern which is more specific. In Go 1.22+ routing, `GET /healthz` registered twice on the same `*http.ServeMux` panics regardless.

The cleanest fix: remove the `GET /healthz` line from `baseMux` entirely, and register a simple fallback in `Handler` (which doesn't use `HandlerFull`) and the rich version in `HandlerFull`. Replace `Handler` to add healthz explicitly:

```go
// Handler builds the minimal REST mux (used only in tests that call Handler directly).
func Handler(c *core.Core, st *state.Store) http.Handler {
	mux := baseMux(c, st)
	return mux
}
```

And `baseMux` removes `/healthz`. Then `sensorMux` (called by `HandlerFull`) adds it after. The cleanest decomposition: keep `baseMux` without `/healthz`; add `GET /healthz` in the layer that knows whether it's enriched or not. Since `Handler` (used in existing tests) doesn't need the rich healthz, add the simple fallback there. Since `HandlerFull` adds the rich one, add it there.

Full replacement for the relevant functions in `httpapi.go`:

```go
func Handler(c *core.Core, st *state.Store) http.Handler {
	mux := baseMux(c, st)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	return mux
}
```

`baseMux` loses the `/healthz` line. `sensorMux` (which calls `baseMux` and is called by `HandlerFull`) does not add `/healthz` — `HandlerFull` adds the rich version.

Complete updated `HandlerFull` incorporating all existing routes plus the three new ones:
```go
func HandlerFull(c *core.Core, st *state.Store, d hw.Devices, frames *state.Frames, deps ControlDeps, rec *events.Recorder, tr *health.Tracker) http.Handler {
	mux := sensorMux(c, st, d)

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		snap := st.Snapshot()
		var sensors map[string]health.SensorStatus
		if tr != nil {
			sensors = tr.Snapshot(time.Now())
		} else {
			sensors = map[string]health.SensorStatus{}
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"status":   "ok",
			"uptime_s": snap.UptimeS,
			"sensors":  sensors,
		})
	})

	mux.HandleFunc("GET /schedules", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, deps.GetSchedules())
	})
	mux.HandleFunc("PUT /schedules/{channel}", func(w http.ResponseWriter, r *http.Request) {
		ch := r.PathValue("channel")
		if ch != "light" && ch != "pump" {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown channel"})
			return
		}
		var s config.Schedule
		if err := json.NewDecoder(r.Body).Decode(&s); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
			return
		}
		if err := s.Validate(); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if err := deps.PutSchedule(ch, s); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "saved"})
	})
	mux.HandleFunc("POST /schedule/{channel}/enabled", func(w http.ResponseWriter, r *http.Request) {
		ch := r.PathValue("channel")
		if ch != "light" && ch != "pump" {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown channel"})
			return
		}
		var body struct {
			Enabled bool `json:"enabled"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
			return
		}
		if err := deps.SetScheduleEnabled(ch, body.Enabled); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		st.SetScheduleEnabled(ch, body.Enabled)
		writeJSON(w, http.StatusOK, map[string]bool{"enabled": body.Enabled})
	})
	mux.HandleFunc("POST /water/low-threshold", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			CM float64 `json:"cm"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.CM < 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "cm must be >= 0"})
			return
		}
		if err := deps.SetWaterLowCM(body.CM); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]float64{"cm": body.CM})
	})

	serveFrame := func(get func() []byte) http.HandlerFunc {
		return func(w http.ResponseWriter, _ *http.Request) {
			b := get()
			if len(b) == 0 {
				http.Error(w, "no frame yet", http.StatusServiceUnavailable)
				return
			}
			w.Header().Set("Content-Type", "image/jpeg")
			w.Header().Set("Cache-Control", "no-store")
			_, _ = w.Write(b)
		}
	}
	mux.HandleFunc("GET /camera/upper.jpg", serveFrame(frames.Upper))
	mux.HandleFunc("GET /camera/lower.jpg", serveFrame(frames.Lower))

	mux.HandleFunc("GET /events", func(w http.ResponseWriter, _ *http.Request) {
		var snap []events.Event
		if rec != nil {
			snap = rec.Snapshot()
		}
		if snap == nil {
			snap = []events.Event{}
		}
		writeJSON(w, http.StatusOK, snap)
	})

	mux.HandleFunc("GET /metrics", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		w.WriteHeader(http.StatusOK)
		writeMetrics(w, st, rec)
	})

	return mux
}
```

Add the `writeMetrics` helper function at the bottom of `httpapi.go`. It hand-rolls Prometheus text format — `# HELP`, `# TYPE`, then one value line per metric. Counters are derived by scanning the event recorder snapshot:

```go
// writeMetrics writes the Prometheus text exposition format to w.
// Nil sensor pointer fields in the snapshot are silently skipped.
func writeMetrics(w http.ResponseWriter, st *state.Store, rec *events.Recorder) {
	snap := st.Snapshot()
	b := &strings.Builder{}

	gauge := func(name, help string, val float64) {
		fmt.Fprintf(b, "# HELP %s %s\n# TYPE %s gauge\n%s %.6g\n", name, help, name, name, val)
	}
	boolGauge := func(name, help string, v bool) {
		f := 0.0
		if v {
			f = 1.0
		}
		gauge(name, help, f)
	}
	counter := func(name, help string, val int64) {
		fmt.Fprintf(b, "# HELP %s %s\n# TYPE %s counter\n%s_total %d\n", name, help, name, name, val)
	}

	// Gauges from state snapshot.
	if snap.Sensors.TemperatureC != nil {
		gauge("gardynd_temperature_c", "Ambient temperature in Celsius.", *snap.Sensors.TemperatureC)
	}
	if snap.Sensors.HumidityPct != nil {
		gauge("gardynd_humidity_pct", "Ambient relative humidity percent.", *snap.Sensors.HumidityPct)
	}
	if snap.Sensors.PCBTempC != nil {
		gauge("gardynd_pcb_temp_c", "PCB temperature in Celsius.", *snap.Sensors.PCBTempC)
	}
	if snap.Sensors.WaterLevelCM != nil {
		gauge("gardynd_water_level_cm", "Distance sensor reading in cm (higher = lower water).", *snap.Sensors.WaterLevelCM)
	}
	if snap.Sensors.Pump != nil {
		gauge("gardynd_pump_bus_voltage", "Pump INA219 bus voltage in volts.", snap.Sensors.Pump.BusVoltage)
		gauge("gardynd_pump_current", "Pump INA219 current in amps.", snap.Sensors.Pump.Current)
		gauge("gardynd_pump_power", "Pump INA219 power in watts.", snap.Sensors.Pump.Power)
	}
	gauge("gardynd_uptime_seconds", "Seconds since gardynd started.", float64(snap.UptimeS))
	boolGauge("gardynd_pump_on", "1 if the pump is currently on.", snap.Pump.On)
	boolGauge("gardynd_light_on", "1 if the light is currently on.", snap.Light.On)
	boolGauge("gardynd_water_low", "1 if the water level is below the low threshold.", snap.Water.Low)
	boolGauge("gardynd_overtemp", "1 if an over-temperature condition is active.", snap.OverTemp)

	// Counters derived from the event ring buffer.
	var pumpRuns, interlockBlocks, failsafes, overtempEvents int64
	if rec != nil {
		for _, ev := range rec.Snapshot() {
			switch ev.Kind {
			case "pump_on":
				pumpRuns++
			case "interlock_block":
				interlockBlocks++
			case "pump_failsafe":
				failsafes++
			case "overtemp":
				overtempEvents++
			}
		}
	}
	counter("gardynd_pump_runs", "Total number of pump-on events since start (approximate: counts ring buffer window).", pumpRuns)
	counter("gardynd_interlock_blocks", "Total pump-on attempts blocked by the water-low interlock.", interlockBlocks)
	counter("gardynd_failsafe", "Total pump failsafe activations.", failsafes)
	counter("gardynd_overtemp_events", "Total over-temperature events recorded.", overtempEvents)

	_, _ = fmt.Fprint(w, b.String())
}
```

Note on `counter` helper: it emits the `_total` suffix on the value line (Prometheus convention) and uses `name` (without `_total`) in the `# HELP`/`# TYPE` lines. The metric name passed in already has the `gardynd_` prefix.

- [ ] **Step 4: Run new tests to verify they pass**

Run: `go test ./internal/httpapi/ -run 'TestGetEvents|TestGetMetrics|TestGetHealthzRich' -v`
Expected:
```
--- PASS: TestGetEvents (0.00s)
--- PASS: TestGetMetrics (0.00s)
--- PASS: TestGetHealthzRich (0.00s)
PASS
```

- [ ] **Step 5: Run full httpapi suite to confirm no regressions**

Update the existing test helpers in `httpapi_test.go` that call `HandlerFull` without the two new arguments. Each existing test that calls `HandlerFull(c, st, mock.New(), state.NewFrames(), deps)` must be updated to `HandlerFull(c, st, mock.New(), state.NewFrames(), deps, nil, nil)`. The tests affected are: `TestSchedulePutGetAndEnable`, `TestScheduleUnknownChannel404`, `TestScheduleInvalidAction400`, `TestWaterThresholdEndpoint`, `TestCameraEndpoint`. Apply this mechanical change before running:

```
# Find every HandlerFull call in httpapi_test.go with 5 args and add nil, nil
```

For example, `TestSchedulePutGetAndEnable` line:
```go
h := HandlerFull(c, st, mock.New(), state.NewFrames(), deps, nil, nil)
```

Do the same for all other `HandlerFull` call sites in the test file.

Run: `go test ./internal/httpapi/ -race -v`
Expected: all existing tests PASS plus three new ones, no races.

---

### Task 7: Structured logging — migrate `log.Printf` → `slog`

**Depends on:** Task 1 (config.ParseLogLevel must exist; this task replaces log calls that import `log`)

This task replaces all `log.Printf` / `log.Fatalf` calls across five source files with `slog` equivalents. It is otherwise independent of Tasks 2–6 (no shared interfaces).

**Files:**
- Modify: `cmd/gardynd/main.go`
- Modify: `internal/core/core.go`
- Modify: `internal/publish/publish.go`
- Modify: `internal/discovery/discovery.go`
- Modify: `internal/hw/real/real.go`

**Design:** In `main.go`, immediately after loading config, configure the slog default:
```go
slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
    Level: config.ParseLogLevel(cfg.LogLevel),
})))
```
All packages then call `slog.Info`, `slog.Warn`, `slog.Error` on the default logger. No per-package logger construction — keep it simple.

- [ ] **Step 1: Configure slog default in main.go**

In `cmd/gardynd/main.go`, replace the `"log"` import with `"log/slog"` (keep `"log"` only if `log.Fatalf` is still used for startup failures — `log.Fatalf` can remain for the three pre-slog-setup fatal errors). Add `"log/slog"` to the import block.

After `cfg, err := config.Load(...)` succeeds, add:
```go
slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
    Level: config.ParseLogLevel(cfg.LogLevel),
})))
```

Replace the two `log.Printf` calls in `main.go` with `slog` equivalents:
```go
// was: log.Printf("REST listening on %s", addr)
slog.Info("REST listening", "addr", addr)

// was: log.Printf("shutting down")
slog.Info("shutting down")
```

The three `log.Fatalf` calls (before slog is configured) remain as `log.Fatalf` — they run before the slog handler is set up and are appropriate for terminal startup failures.

- [ ] **Step 2: Replace log.Printf in core.go**

In `internal/core/core.go`, replace `"log"` import with `"log/slog"`.

Replace each `log.Printf` call:
```go
// was: log.Printf("light on: %v", err)
slog.Error("light on", "err", err)

// was: log.Printf("light off: %v", err)
slog.Error("light off", "err", err)

// was: log.Printf("light level: %v", err)
slog.Error("light level", "err", err)

// was: log.Printf("pump on: %v", err)
slog.Error("pump on", "err", err)

// was: log.Printf("pump off: %v", err)
slog.Error("pump off", "err", err)

// was: log.Printf("pump level: %v", err)
slog.Error("pump level", "err", err)

// was: log.Printf("pump failsafe: forced off after %s", maxRT)
slog.Warn("pump failsafe: forced off", "after", maxRT)
```

- [ ] **Step 3: Replace log.Printf in publish.go**

In `internal/publish/publish.go`, replace `"log"` import with `"log/slog"`.

Replace each `log.Printf` call:
```go
// was: log.Printf("env read: %v", err)
slog.Warn("env read failed", "err", err)

// was: log.Printf("upper camera: %v", err)
slog.Warn("upper camera capture failed", "err", err)

// was: log.Printf("lower camera: %v", err)
slog.Warn("lower camera capture failed", "err", err)

// was: log.Printf("publish: invalid sensor interval %v, defaulting to 30m", p.interval)
slog.Warn("invalid sensor interval, defaulting to 30m", "interval", p.interval)

// was: log.Printf("publish: invalid camera interval %v, defaulting to 1h", interval)
slog.Warn("invalid camera interval, defaulting to 1h", "interval", interval)
```

- [ ] **Step 4: Replace log.Printf in discovery.go**

In `internal/discovery/discovery.go`, replace `"log"` import with `"log/slog"`.

Replace the one call:
```go
// was: log.Printf("zeroconf advertise failed (continuing): %v", err)
slog.Warn("zeroconf advertise failed, continuing", "err", err)
```

- [ ] **Step 5: Replace log.Printf in real.go**

In `internal/hw/real/real.go`, replace `"log"` import with `"log/slog"`.

Replace each `log.Printf` call:
```go
// was: log.Printf("i2c bus unavailable: %v (sensors disabled)", err)
slog.Warn("i2c bus unavailable, sensors disabled", "err", err)

// was: log.Printf("pct2075 init failed: %v", err)
slog.Warn("pct2075 init failed", "err", err)

// was: log.Printf("upper camera (%s @ %s) init failed: %v", cfg.Camera.UpperDevice, cfg.Camera.Resolution, err)
slog.Warn("upper camera init failed", "device", cfg.Camera.UpperDevice, "resolution", cfg.Camera.Resolution, "err", err)

// was: log.Printf("lower camera (%s @ %s) init failed: %v", cfg.Camera.LowerDevice, cfg.Camera.Resolution, err)
slog.Warn("lower camera init failed", "device", cfg.Camera.LowerDevice, "resolution", cfg.Camera.Resolution, "err", err)

// was: log.Printf("button init failed: %v", err)
slog.Warn("button init failed", "err", err)
```

- [ ] **Step 6: Build to confirm no compile errors**

Run: `go build ./...`
Expected: builds with no errors. (All `log.Printf` calls have been replaced; no unused imports remain.)

---

### Task 8: Main wiring — events, health tracker, slog level

**Depends on:** Tasks 3 (SetEvents), 5 (SetHealthTracker), 6 (HandlerFull new signature), 7 (slog in main already done in Task 7). Tasks 3 and 5 can complete in parallel. Task 6 must complete before this task (HandlerFull signature change).

**Files:**
- Modify: `cmd/gardynd/main.go`

Wire the `events.Recorder` and `health.Tracker` into the process: create them, call `c.SetEvents(rec)`, call `pub.SetHealthTracker(tr)`, pass both to `HandlerFull`. The slog default setup from Task 7 Step 1 is already done.

- [ ] **Step 1: Wire events.Recorder and health.Tracker in main.go**

Add imports to `cmd/gardynd/main.go`:
```go
"github.com/iot-root/garden-of-eden/internal/events"
"github.com/iot-root/garden-of-eden/internal/health"
```

After `c := core.New(devs, st)` and before `go c.Run()`, create the recorder and wire it:
```go
rec := events.NewRecorder(100)
c.SetEvents(rec)
```

After `pub := publish.New(...)` and before `go pub.Run()`, create the tracker and wire it:
```go
tr := health.NewTracker()
pub.SetHealthTracker(tr)
```

Update the `HandlerFull` call to pass the two new arguments:
```go
server := &http.Server{
    Addr:    addr,
    Handler: httpapi.HandlerFull(c, st, devs, frames, deps, rec, tr),
}
```

The complete relevant section of `cmd/gardynd/main.go` after this change:
```go
st := state.New()
c := core.New(devs, st)

rec := events.NewRecorder(100)
c.SetEvents(rec)

go c.Run()
defer c.Stop()

// Core runtime config.
c.SetWaterLowCM(cfg.Water.LowCM)
c.SetPumpMaxRuntime(10 * time.Minute)
c.SetCutLightOnOverTemp(cfg.OverTemp.CutLight)

// ... (schedules, sched.Run, onOverTemp wiring unchanged) ...

frames := state.NewFrames()
pub := publish.New(devs, st, frames, time.Duration(cfg.TelemetryIntervalSeconds)*time.Second, onOverTemp)

tr := health.NewTracker()
pub.SetHealthTracker(tr)

go pub.Run()
go pub.RunCameras(time.Duration(cfg.Camera.IntervalSeconds) * time.Second)
defer pub.Stop()

// ... (button, discovery unchanged) ...

deps := httpapi.ControlDeps{ ... } // unchanged

addr := fmt.Sprintf(":%d", cfg.HTTP.Port)
server := &http.Server{Addr: addr, Handler: httpapi.HandlerFull(c, st, devs, frames, deps, rec, tr)}
```

- [ ] **Step 2: Build both targets**

Run: `go build ./cmd/gardynd/ && GOARCH=arm64 GOOS=linux go build -o /dev/null ./cmd/gardynd/`
Expected: both builds succeed.

---

### Task 9: Full test suite + single commit

**Depends on:** Tasks 1–8 all complete.

**Files:** none (validation only + commit)

- [ ] **Step 1: Run the full test suite with race detector**

Run: `go test ./... -race`
Expected: all packages PASS, no data races.
```
ok  	github.com/iot-root/garden-of-eden/internal/config   (cached)
ok  	github.com/iot-root/garden-of-eden/internal/core     (cached)
ok  	github.com/iot-root/garden-of-eden/internal/events
ok  	github.com/iot-root/garden-of-eden/internal/health
ok  	github.com/iot-root/garden-of-eden/internal/httpapi  (cached)
ok  	github.com/iot-root/garden-of-eden/internal/publish  (cached)
ok  	github.com/iot-root/garden-of-eden/internal/state    (cached)
PASS
```

- [ ] **Step 2: Smoke-test the mock binary**

Run `./bin/gardynd --hw=mock` in one terminal. In another:
```bash
# Confirm slog structured output on stderr (not old log.Printf format).
# Confirm /events returns [] initially.
curl -s http://localhost:5000/events | python3 -m json.tool

# Turn pump on, then check events.
curl -s -X POST http://localhost:5000/pump/on
curl -s http://localhost:5000/events | python3 -m json.tool
# Expect: [{"time":"...","kind":"pump_on","detail":"speed=100"}]

# Confirm /metrics returns Prometheus text.
curl -s http://localhost:5000/metrics | head -20
# Expect: lines starting with # HELP and gardynd_ prefixes.

# Confirm /healthz returns per-sensor data.
curl -s http://localhost:5000/healthz | python3 -m json.tool
# Expect: {"status":"ok","uptime_s":N,"sensors":{"env":{...},...}}
```

- [ ] **Step 3: Single commit**

```bash
git add \
  internal/config/config.go internal/config/config_test.go \
  internal/events/events.go internal/events/events_test.go \
  internal/core/core.go internal/core/core_test.go \
  internal/health/health.go internal/health/health_test.go \
  internal/publish/publish.go internal/publish/publish_test.go \
  internal/httpapi/httpapi.go internal/httpapi/httpapi_test.go \
  internal/hw/real/real.go \
  internal/discovery/discovery.go \
  cmd/gardynd/main.go
git commit -m "feat: structured logging (slog), event ring buffer + GET /events, hand-rolled GET /metrics, richer GET /healthz"
```

---

## Self-Review

**1. Spec coverage**

| Requirement | Task |
|---|---|
| Structured logging via `log/slog`, no new dep | Tasks 7, 1 |
| `LogLevel` config field + env `LOG_LEVEL`, default "info" | Task 1 |
| `slog.SetDefault` with text handler in main | Task 7 Step 1 |
| Replace `log.Printf` in core, publish, discovery, real, main | Task 7 Steps 1–5 |
| `internal/events` ring `Recorder`, capacity 100, `Record`/`Snapshot` | Task 2 |
| `Event{Time, Kind, Detail}` with json tags | Task 2 |
| `Core.SetEvents(*events.Recorder)` setter | Task 3 |
| Record `pump_on`, `pump_off` in `applyPump` | Task 3 |
| Record `interlock_block` in `applyPump` when water low | Task 3 |
| Record `pump_failsafe` in `armPumpFailsafe` | Task 3 |
| Record `overtemp`, `overtemp_clear` in `applyOverTemp` | Task 3 |
| `GET /events` returning JSON snapshot | Task 6 |
| `GET /metrics` Prometheus text, `gardynd_` prefix, no new dep | Task 6 |
| `# HELP` / `# TYPE` per metric, `Content-Type: text/plain; version=0.0.4` | Task 6 |
| Gauges: temperature, humidity, pcb_temp, water_level, pump power (bus_voltage, current, power), uptime, pump_on, light_on, water_low, overtemp | Task 6 |
| Skip nil sensor pointers | Task 6 |
| Counters: pump_runs_total, interlock_blocks_total, failsafe_total, overtemp_events_total | Task 6 |
| `internal/health` Tracker, `Touch`, `SensorAge`, `Snapshot`, staleness window 120s | Task 4 |
| Publisher calls `tracker.Touch(name)` after each successful read | Task 5 |
| `GET /healthz` returns `{status, uptime_s, sensors: {name: {ok, age_s}}}` | Task 6 |
| Main wires events.Recorder, health.Tracker; slog configured at startup | Tasks 7, 8 |
| Single commit at the end | Task 9 |

No gaps identified.

**2. Placeholder scan**

No "TBD", "TODO", "implement later", or "similar to Task N" text. Every code step contains complete compilable Go. Every test step has the exact `go test` command and expected output. The `writeMetrics` function is fully written out including all metric names and help strings.

**3. Type consistency**

- `events.NewRecorder(100)` / `events.Event{Time, Kind, Detail}` / `rec.Record(kind, detail string)` / `rec.Snapshot() []Event` — defined in Task 2, used identically in Tasks 3, 6, 8, 9.
- `c.SetEvents(*events.Recorder)` — defined in Task 3, called in Task 8.
- `health.NewTracker()` / `tr.Touch(name string)` / `tr.Snapshot(now time.Time) map[string]SensorStatus` / `health.SensorStatus{OK bool, AgeS float64}` — defined in Task 4, used identically in Tasks 5, 6, 8, 9.
- `pub.SetHealthTracker(*health.Tracker)` — defined in Task 5, called in Task 8.
- `HandlerFull(c, st, d, frames, deps, rec *events.Recorder, tr *health.Tracker)` — signature locked in Task 6; existing call sites in `httpapi_test.go` updated to pass `nil, nil`; `main.go` updated in Task 8 to pass real values.
- `config.ParseLogLevel(s string) slog.Level` — defined in Task 1, called in Task 7 Step 1.
- `publish.Publisher.publishOnce()` — already lowercase (package-internal), accessible from `publish_test.go` (same package `publish`). No change to method visibility needed.
- `health.Tracker.TouchAt(name string, at time.Time)` — defined in Task 4 (for test injection), used only in `health_test.go`.
- `config.Config.TelemetryIntervalSeconds` — this field is referenced in main.go in the Plan 6 wiring (`time.Duration(cfg.TelemetryIntervalSeconds)*time.Second`). Plan 7 does not add this field again; it already exists post-Plan-6. The dependency note (`Depends on: Plan 6`) covers this.

**4. Dependency audit**

- **Task 1** (`internal/config/`): no other plan-7 task modifies config. Depends on none within this plan. ✓
- **Task 2** (`internal/events/`): new package, no peer-task interfaces. Depends on none. ✓
- **Task 3** (`internal/core/`): imports `internal/events` — must run after Task 2. Marked `Depends on: Task 2`. ✓
- **Task 4** (`internal/health/`): new package, no peer-task interfaces. Depends on none. ✓ Tasks 2 and 4 are mutually independent and may be implemented in parallel.
- **Task 5** (`internal/publish/`): imports `internal/health` — must run after Task 4. Marked `Depends on: Task 4`. ✓
- **Task 6** (`internal/httpapi/`): imports `internal/events` (Task 2) and `internal/health` (Task 4). Both must be complete. Marked `Depends on: Task 2, Task 4`. ✓
- **Task 7** (logging, multi-file): `main.go` uses `config.ParseLogLevel` (Task 1). `core.go`, `publish.go`, `discovery.go`, `real.go` just swap import from `log` to `log/slog` — no inter-task interface dependency beyond the stdlib. The only real dependency is Task 1 for `ParseLogLevel` used in main. Marked `Depends on: Task 1`. ✓
- **Task 8** (`cmd/gardynd/main.go` wiring): uses `c.SetEvents` (Task 3), `pub.SetHealthTracker` (Task 5), `HandlerFull` with new signature (Task 6), slog setup (Task 7 Step 1). Marked `Depends on: Tasks 3, 5, 6, 7`. ✓
- **Task 9** (full suite + commit): depends on all tasks complete. ✓

**Parallel opportunities:**
- Tasks 1, 2, and 4 are fully independent and can run in parallel.
- Task 3 can start as soon as Task 2 completes (unblocked by Task 1).
- Task 5 can start as soon as Task 4 completes (unblocked by Tasks 1–3).
- Task 6 can start as soon as both Tasks 2 and 4 complete.
- Task 7 can start as soon as Task 1 completes (the `main.go` slog setup depends on `ParseLogLevel`; the other four files are pure import swaps with no inter-task interface).
- Task 8 must wait for Tasks 3, 5, 6, and 7 to all complete.
