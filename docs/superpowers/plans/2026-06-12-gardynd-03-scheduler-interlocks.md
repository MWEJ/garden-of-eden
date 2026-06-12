# gardynd Plan 3 — Scheduler, Interlocks & Publishers Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add the behavior that makes gardynd a complete replacement: a state-based scheduler with restart catch-up, the water-low interlock across every pump-on path, a pump max-runtime failsafe, the over-temp monitor, periodic sensor reads into the snapshot store, camera JPEG serving, the schedule/water REST endpoints, and optional zeroconf discovery.

**Architecture:** Behavior touching device state stays in the single-writer `core`; the scheduler, interlock, and failsafe submit/observe through it and write results to the snapshot store. Sensor publishers are goroutines that read hardware and update the store. Cameras write their latest JPEG into a small frame buffer the REST layer serves. No MQTT.

**Tech Stack:** Go stdlib `time`/`net/http`, `gopkg.in/yaml.v3` (atomic save), `github.com/grandcat/zeroconf` (optional mDNS).

**Spec:** `docs/superpowers/specs/2026-06-12-gardynd-go-service-design.md`
**Depends on:** Plan 1 (core, state store, httpapi, config) and Plan 2 (sensor interfaces on `hw.Devices`, sensor REST routes).

---

## File Structure (this plan)

```
internal/config/config.go         MODIFY: schedule + water + overtemp config + atomic Save
internal/config/schedule.go       schedule types + timeline evaluation (pure)
internal/config/schedule_test.go
internal/core/core.go             MODIFY: water-low interlock, pump failsafe, overtemp → store
internal/core/core_test.go        MODIFY: interlock + failsafe tests
internal/core/scheduler.go        scheduler goroutine (uses timeline eval)
internal/core/scheduler_test.go
internal/state/frames.go          latest-JPEG frame buffer (cameras)
internal/publish/publish.go       periodic sensor + camera reads into store/frames
internal/publish/publish_test.go
internal/httpapi/httpapi.go       MODIFY: /schedules CRUD, schedule-enable, water threshold, /camera
internal/httpapi/httpapi_test.go  MODIFY
internal/discovery/discovery.go   optional zeroconf advertiser
cmd/gardynd/main.go               MODIFY: start scheduler + publishers + discovery
```

---

### Task 1: Schedule types + timeline evaluation (pure, unit-tested)

**Depends on:** none (within this plan)

**Files:**
- Create: `internal/config/schedule.go`
- Test: `internal/config/schedule_test.go`

Scheduling is **state-based**: given entries and the current time, compute the
state the timeline implies *now* (the most recent past entry, wrapping
midnight). This enables restart catch-up and is fully testable.

- [ ] **Step 1: Write the failing test**

`internal/config/schedule_test.go`:
```go
package config

import "testing"

func mins(h, m int) int { return h*60 + m }

func lightSched() Schedule {
	return Schedule{Enabled: true, Entries: []ScheduleEntry{
		{At: "06:00", Action: "on", Brightness: 70},
		{At: "09:00", Action: "off"},
		{At: "17:00", Action: "on", Brightness: 50},
		{At: "20:00", Action: "off"},
	}}
}

func TestStateAtMidTimeline(t *testing.T) {
	s := lightSched()
	st, ok := s.StateAt(mins(6, 5))
	if !ok || !st.On || st.Brightness != 70 {
		t.Errorf("06:05 => %+v ok=%v, want on@70", st, ok)
	}
	st, _ = s.StateAt(mins(12, 0))
	if st.On {
		t.Errorf("12:00 => %+v, want off", st)
	}
	st, _ = s.StateAt(mins(18, 0))
	if !st.On || st.Brightness != 50 {
		t.Errorf("18:00 => %+v, want on@50", st)
	}
}

func TestMidnightWrap(t *testing.T) {
	s := lightSched()
	st, _ := s.StateAt(mins(2, 0)) // before first entry => carried from 20:00 off
	if st.On {
		t.Errorf("02:00 => %+v, want off", st)
	}
}

func TestDueEntries(t *testing.T) {
	s := lightSched()
	due := s.DueBetween(mins(5, 59), mins(6, 0))
	if len(due) != 1 || due[0].Action != "on" {
		t.Errorf("due = %+v, want [on@06:00]", due)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config/ -run 'State|Midnight|Due' -v`
Expected: build failure — `undefined: Schedule`.

- [ ] **Step 3: Implement the schedule logic**

`internal/config/schedule.go`:
```go
package config

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

type ScheduleEntry struct {
	At         string `yaml:"at" json:"at"`         // "HH:MM"
	Action     string `yaml:"action" json:"action"` // "on" | "off"
	Brightness int    `yaml:"brightness,omitempty" json:"brightness,omitempty"`
}

type Schedule struct {
	Enabled bool            `yaml:"enabled" json:"enabled"`
	Entries []ScheduleEntry `yaml:"entries" json:"entries"`
}

type Schedules struct {
	Light Schedule `yaml:"light" json:"light"`
	Pump  Schedule `yaml:"pump" json:"pump"`
}

type ChannelState struct {
	On         bool
	Brightness int
}

func minutes(at string) (int, error) {
	parts := strings.SplitN(at, ":", 2)
	if len(parts) != 2 {
		return 0, fmt.Errorf("bad time %q", at)
	}
	h, err1 := strconv.Atoi(parts[0])
	m, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil || h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, fmt.Errorf("bad time %q", at)
	}
	return h*60 + m, nil
}

type parsedEntry struct {
	min   int
	entry ScheduleEntry
}

func (s Schedule) sortedEntries() []parsedEntry {
	var out []parsedEntry
	for _, e := range s.Entries {
		if m, err := minutes(e.At); err == nil {
			out = append(out, parsedEntry{m, e})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].min < out[j].min })
	return out
}

// StateAt returns the channel state implied at nowMin (minutes since midnight).
func (s Schedule) StateAt(nowMin int) (ChannelState, bool) {
	es := s.sortedEntries()
	if len(es) == 0 {
		return ChannelState{}, false
	}
	idx := -1
	for i, e := range es {
		if e.min <= nowMin {
			idx = i
		}
	}
	if idx == -1 {
		idx = len(es) - 1 // carried from previous day
	}
	e := es[idx].entry
	return ChannelState{On: e.Action == "on", Brightness: e.Brightness}, true
}

// DueBetween returns entries with time in (prevMin, nowMin].
func (s Schedule) DueBetween(prevMin, nowMin int) []ScheduleEntry {
	var out []ScheduleEntry
	for _, e := range s.sortedEntries() {
		if e.min > prevMin && e.min <= nowMin {
			out = append(out, e.entry)
		}
	}
	return out
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/config/ -run 'State|Midnight|Due' -v`
Expected: PASS.

---

### Task 2: Config — schedule/water/overtemp fields + atomic Save

**Depends on:** Task 1

**Files:**
- Modify: `internal/config/config.go`
- Test: `internal/config/config_test.go` (append)

- [ ] **Step 1: Write the failing Save test (append)**

Append to `internal/config/config_test.go`:
```go
func TestAtomicSaveRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	c, _ := Load("")
	c.Schedules.Light = Schedule{Enabled: true, Entries: []ScheduleEntry{{At: "06:00", Action: "on", Brightness: 70}}}
	if err := c.Save(path); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Schedules.Light.Enabled || len(got.Schedules.Light.Entries) != 1 {
		t.Errorf("round-trip lost schedule: %+v", got.Schedules.Light)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config/ -run AtomicSave -v`
Expected: build failure — `c.Save` undefined / `Schedules` field missing.

- [ ] **Step 3: Extend Config and add Save**

Add to the `Config` struct:
```go
	Schedules Schedules      `yaml:"schedules"`
	Water     WaterConfig    `yaml:"water"`
	OverTemp  OverTempConfig `yaml:"overtemp"`
```
Add types:
```go
type WaterConfig struct {
	LowCM float64 `yaml:"low_cm"` // 0 disables the interlock
}

type OverTempConfig struct {
	CutLight bool `yaml:"cut_light"`
}
```
In `applyEnv`, preserve the legacy env knob:
```go
	if v, ok := os.LookupEnv("WATER_LOW_CM"); ok && v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			c.Water.LowCM = f
		}
	}
```
Add the atomic save (temp file + rename, same dir):
```go
import "path/filepath"

func (c Config) Save(path string) error {
	data, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".config-*.yaml")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, path)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/config/ -v`
Expected: PASS.

---

### Task 3: Core — water-low interlock, pump failsafe, over-temp (→ store)

**Depends on:** Task 2, Plan 2 Task 1 (`hw.Devices` sensor fields)

**Files:**
- Modify: `internal/core/core.go`
- Test: `internal/core/core_test.go` (append)

The interlock guards **every** pump-on (REST, button, scheduler) — the
single-writer's job. Results are written to the snapshot store.

- [ ] **Step 1: Write the failing tests (append)**

Append to `internal/core/core_test.go`:
```go
import mockhw "github.com/iot-root/garden-of-eden/internal/hw/mock"

func TestPumpBlockedWhenWaterLow(t *testing.T) {
	st := state.New()
	devs := mockhw.New()
	devs.Distance.(*mockhw.Distance).CM = 12.0 // > threshold => too low
	c := New(devs, st)
	c.SetWaterLowCM(10.0)
	go c.Run()
	defer c.Stop()

	c.Submit(Command{Target: TargetPump, Action: ActionOn})
	time.Sleep(2 * time.Second) // flashLights takes ~1.8s

	if st.Snapshot().Pump.On {
		t.Error("pump turned on despite low water")
	}
	if !st.Snapshot().Water.Low {
		t.Error("water.low not set true")
	}
	if devs.Pump.(*mockhw.Pump).Speed() != 0 {
		t.Error("pump hardware was driven")
	}
}

func TestPumpAllowedWhenWaterOK(t *testing.T) {
	st := state.New()
	devs := mockhw.New()
	devs.Distance.(*mockhw.Distance).CM = 5.0 // <= threshold => ok
	c := New(devs, st)
	c.SetWaterLowCM(10.0)
	go c.Run()
	defer c.Stop()

	c.Submit(Command{Target: TargetPump, Action: ActionOn})
	for i := 0; i < 50; i++ {
		if st.Snapshot().Pump.On {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Error("pump should have turned on")
}

func TestPumpFailsafeForcesOff(t *testing.T) {
	st := state.New()
	c := New(mockhw.New(), st)
	c.SetPumpMaxRuntime(50 * time.Millisecond)
	go c.Run()
	defer c.Stop()

	c.Submit(Command{Target: TargetPump, Action: ActionOn})
	for i := 0; i < 100; i++ {
		if !st.Snapshot().Pump.On {
			return // failsafe turned it off
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("failsafe did not turn pump off")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/core/ -run 'Water|Failsafe' -v`
Expected: build failure — `SetWaterLowCM`/`SetPumpMaxRuntime` undefined.

- [ ] **Step 3: Implement in core**

Add fields + setters to `Core`/`New`:
```go
import "time"

// fields: waterLowCM float64; pumpMaxRuntime time.Duration; pumpTimer *time.Timer; cutLightOnOverTemp bool

func (c *Core) SetWaterLowCM(cm float64) {
	c.waterLowCM = cm
	c.store.SetWater(cm, false)
}
func (c *Core) SetPumpMaxRuntime(d time.Duration)  { c.pumpMaxRuntime = d }
func (c *Core) SetCutLightOnOverTemp(b bool)       { c.cutLightOnOverTemp = b }
func (c *Core) waterLowEnabled() bool              { return c.waterLowCM > 0 }
```
Rewrite `applyPump` `ActionOn` to enforce the interlock + arm the failsafe:
```go
	case ActionOn:
		if c.waterLowEnabled() {
			cm, err := c.measureDistance()
			if err == nil && cm > c.waterLowCM {
				c.store.SetWater(c.waterLowCM, true) // low=true
				c.flashLights()
				return
			}
			c.store.SetWater(c.waterLowCM, false)
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
```
In `applyPump` `ActionOff`, disarm first:
```go
	case ActionOff:
		c.disarmPumpFailsafe()
		if err := c.dev.Pump.Off(); err != nil {
			log.Printf("pump off: %v", err)
			return
		}
		c.store.SetPump(false, c.pumpLevel)
```
Add helpers (run on the core goroutine; no extra locking):
```go
func (c *Core) measureDistance() (float64, error) {
	if c.dev.Distance == nil {
		return 0, fmt.Errorf("no distance sensor")
	}
	return c.dev.Distance.MeasureCM()
}

func (c *Core) flashLights() {
	if c.dev.Light == nil {
		return
	}
	prev := c.dev.Light.Brightness()
	for i := 0; i < 3; i++ {
		_ = c.dev.Light.Off()
		time.Sleep(300 * time.Millisecond)
		_ = c.dev.Light.SetBrightness(100)
		time.Sleep(300 * time.Millisecond)
	}
	_ = c.dev.Light.SetBrightness(prev)
}

func (c *Core) armPumpFailsafe() {
	if c.pumpMaxRuntime <= 0 {
		return
	}
	c.disarmPumpFailsafe()
	c.pumpTimer = time.AfterFunc(c.pumpMaxRuntime, func() {
		c.Submit(Command{Target: TargetPump, Action: ActionOff})
		log.Printf("pump failsafe: forced off after %s", c.pumpMaxRuntime)
	})
}

func (c *Core) disarmPumpFailsafe() {
	if c.pumpTimer != nil {
		c.pumpTimer.Stop()
		c.pumpTimer = nil
	}
}
```
Add over-temp handling via a new target the monitor submits:
```go
const TargetOverTemp Target = 2

// in apply(): case TargetOverTemp: c.applyOverTemp(cmd)  // Value 1=alert, 0=clear

func (c *Core) applyOverTemp(cmd Command) {
	alert := cmd.Value == 1
	c.store.SetOverTemp(alert)
	if alert && c.cutLightOnOverTemp && c.dev.Light != nil {
		_ = c.dev.Light.Off()
		c.store.SetLight(false, c.lightLevel)
	}
}
```
Add `"fmt"` to imports.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/core/ -v`
Expected: PASS.

> Efficiency note: `flashLights` blocks the core ~1.8s during a low-water warning
> (matches the Python behavior and is desirable — no pump-on should proceed). Do
> not background it, or the interlock would race.

---

### Task 4: Scheduler goroutine

**Depends on:** Task 3 (core setters), Task 1 (timeline eval)

**Files:**
- Create: `internal/core/scheduler.go`
- Test: `internal/core/scheduler_test.go`

- [ ] **Step 1: Write the failing test**

`internal/core/scheduler_test.go`:
```go
package core

import (
	"testing"
	"time"

	"github.com/iot-root/garden-of-eden/internal/config"
	mockhw "github.com/iot-root/garden-of-eden/internal/hw/mock"
	"github.com/iot-root/garden-of-eden/internal/state"
)

func TestSchedulerCatchUpAppliesCurrentState(t *testing.T) {
	st := state.New()
	devs := mockhw.New()
	c := New(devs, st)
	go c.Run()
	defer c.Stop()

	sched := config.Schedule{Enabled: true, Entries: []config.ScheduleEntry{
		{At: "06:00", Action: "on", Brightness: 70},
		{At: "20:00", Action: "off"},
	}}
	s := NewScheduler(c, func() config.Schedules { return config.Schedules{Light: sched} })
	s.CatchUpAt(12 * 60) // noon => on@70

	for i := 0; i < 50; i++ {
		if st.Snapshot().Light.Brightness == 70 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Error("catch-up did not set brightness to 70")
}

func TestSchedulerSkipsDisabledChannel(t *testing.T) {
	st := state.New()
	c := New(mockhw.New(), st)
	go c.Run()
	defer c.Stop()
	sched := config.Schedule{Enabled: false, Entries: []config.ScheduleEntry{
		{At: "06:00", Action: "on", Brightness: 70},
	}}
	s := NewScheduler(c, func() config.Schedules { return config.Schedules{Light: sched} })
	s.CatchUpAt(12 * 60)

	time.Sleep(100 * time.Millisecond)
	if st.Snapshot().Light.Brightness != 0 {
		t.Error("disabled schedule should not drive the light")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/core/ -run Scheduler -v`
Expected: build failure — `undefined: NewScheduler`.

- [ ] **Step 3: Implement the scheduler**

`internal/core/scheduler.go`:
```go
package core

import (
	"time"

	"github.com/iot-root/garden-of-eden/internal/config"
)

type Scheduler struct {
	core    *Core
	schedFn func() config.Schedules
	done    chan struct{}
}

func NewScheduler(c *Core, schedFn func() config.Schedules) *Scheduler {
	return &Scheduler{core: c, schedFn: schedFn, done: make(chan struct{})}
}

func nowMinutes(t time.Time) int { return t.Hour()*60 + t.Minute() }

func (s *Scheduler) CatchUpAt(nowMin int) {
	sc := s.schedFn()
	s.applyState(TargetLight, sc.Light, nowMin)
	s.applyState(TargetPump, sc.Pump, nowMin)
}

func (s *Scheduler) applyState(target Target, sch config.Schedule, nowMin int) {
	if !sch.Enabled {
		return
	}
	st, ok := sch.StateAt(nowMin)
	if !ok {
		return
	}
	if st.On {
		s.core.Submit(Command{Target: target, Action: ActionOn, Value: st.Brightness})
	} else {
		s.core.Submit(Command{Target: target, Action: ActionOff})
	}
}

// Run does an initial catch-up then fires due entries each minute boundary.
func (s *Scheduler) Run() {
	s.CatchUpAt(nowMinutes(time.Now()))
	prev := nowMinutes(time.Now())
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-s.done:
			return
		case now := <-ticker.C:
			cur := nowMinutes(now)
			if cur == prev {
				continue
			}
			sc := s.schedFn()
			s.fireDue(TargetLight, sc.Light, prev, cur)
			s.fireDue(TargetPump, sc.Pump, prev, cur)
			prev = cur
		}
	}
}

func (s *Scheduler) fireDue(target Target, sch config.Schedule, prev, cur int) {
	if !sch.Enabled {
		return
	}
	for _, e := range sch.DueBetween(prev, cur) {
		if e.Action == "on" {
			s.core.Submit(Command{Target: target, Action: ActionOn, Value: e.Brightness})
		} else {
			s.core.Submit(Command{Target: target, Action: ActionOff})
		}
	}
}

func (s *Scheduler) Stop() { close(s.done) }
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/core/ -run Scheduler -v`
Expected: PASS.

---

### Task 5: Periodic sensor + camera publishers (→ store/frames)

**Depends on:** Plan 2 Task 1 (sensors), Task 6 (frame buffer), Plan 1 state store

**Files:**
- Create: `internal/state/frames.go`
- Create: `internal/publish/publish.go`
- Test: `internal/publish/publish_test.go`

Publishers read sensors on an interval and write into the snapshot store; the
camera publisher writes the latest JPEG into a frame buffer the REST layer
serves.

- [ ] **Step 1: Implement the frame buffer**

`internal/state/frames.go`:
```go
package state

import "sync"

// Frames holds the latest JPEG per camera.
type Frames struct {
	mu    sync.RWMutex
	upper []byte
	lower []byte
}

func NewFrames() *Frames { return &Frames{} }

func (f *Frames) SetUpper(b []byte) { f.mu.Lock(); f.upper = b; f.mu.Unlock() }
func (f *Frames) SetLower(b []byte) { f.mu.Lock(); f.lower = b; f.mu.Unlock() }

func (f *Frames) Upper() []byte { f.mu.RLock(); defer f.mu.RUnlock(); return f.upper }
func (f *Frames) Lower() []byte { f.mu.RLock(); defer f.mu.RUnlock(); return f.lower }
```

- [ ] **Step 2: Write the failing publisher test**

`internal/publish/publish_test.go`:
```go
package publish

import (
	"testing"

	"github.com/iot-root/garden-of-eden/internal/hw/mock"
	"github.com/iot-root/garden-of-eden/internal/state"
)

func TestPublishOnceUpdatesStore(t *testing.T) {
	devs := mock.New()
	st := state.New()
	frames := state.NewFrames()
	p := New(devs, st, frames, 0)
	p.publishOnce()

	snap := st.Snapshot()
	if snap.Sensors.TemperatureC == nil || snap.Sensors.HumidityPct == nil {
		t.Error("temperature/humidity not set in snapshot")
	}
	if snap.Sensors.WaterLevelCM == nil {
		t.Error("water level not set")
	}
	if snap.Sensors.Pump == nil {
		t.Error("pump power not set")
	}
}

func TestCaptureUpdatesFrames(t *testing.T) {
	devs := mock.New()
	p := New(devs, state.New(), state.NewFrames(), 0)
	p.captureOnce()
	if len(p.frames.Upper()) == 0 {
		t.Error("upper frame not captured")
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/publish/ -v`
Expected: build failure — `undefined: New`.

- [ ] **Step 4: Implement the publishers**

`internal/publish/publish.go`:
```go
// Package publish runs periodic sensor and camera reads, writing results into
// the snapshot store and the camera frame buffer.
package publish

import (
	"log"
	"time"

	"github.com/iot-root/garden-of-eden/internal/hw"
	"github.com/iot-root/garden-of-eden/internal/state"
)

type Publisher struct {
	dev      hw.Devices
	store    *state.Store
	frames   *state.Frames
	interval time.Duration
	done     chan struct{}
}

func New(dev hw.Devices, store *state.Store, frames *state.Frames, interval time.Duration) *Publisher {
	return &Publisher{dev: dev, store: store, frames: frames, interval: interval, done: make(chan struct{})}
}

func (p *Publisher) publishOnce() {
	if p.dev.Env != nil {
		if t, h, err := p.dev.Env.Read(); err == nil {
			p.store.SetTemperature(t)
			p.store.SetHumidity(h)
		} else {
			log.Printf("env read: %v", err)
		}
	}
	if p.dev.PCBTemp != nil {
		if t, err := p.dev.PCBTemp.Temperature(); err == nil {
			p.store.SetPCBTemp(t)
		}
	}
	if p.dev.Distance != nil {
		if cm, err := p.dev.Distance.MeasureCM(); err == nil {
			p.store.SetWaterLevel(cm)
		}
	}
	if p.dev.Power != nil {
		if r, err := p.dev.Power.Read(); err == nil {
			p.store.SetPumpPower(state.PumpPower{BusVoltage: r.BusVoltage, Current: r.Current, Power: r.Power})
		}
	}
}

func (p *Publisher) captureOnce() {
	if p.dev.UpperCamera != nil {
		if b, err := p.dev.UpperCamera.Capture(); err == nil {
			p.frames.SetUpper(b)
		} else {
			log.Printf("upper camera: %v", err)
		}
	}
	if p.dev.LowerCamera != nil {
		if b, err := p.dev.LowerCamera.Capture(); err == nil {
			p.frames.SetLower(b)
		} else {
			log.Printf("lower camera: %v", err)
		}
	}
}

// Run publishes sensors immediately then every interval.
func (p *Publisher) Run() {
	p.publishOnce()
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

func (p *Publisher) Stop() { close(p.done) }
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/publish/ -v`
Expected: PASS.

---

### Task 6: REST — `/schedules` CRUD, schedule-enable, water threshold, `/camera`

**Depends on:** Task 1 (schedule types), Task 2 (Save), Task 5 (frames), Plan 2 sensor routes

**Files:**
- Modify: `internal/httpapi/httpapi.go`
- Test: `internal/httpapi/httpapi_test.go` (append)

Adds the control endpoints that replace what MQTT did, plus camera JPEG serving.
Uses callbacks so httpapi stays decoupled from config persistence.

- [ ] **Step 1: Write the failing tests (append)**

Append to `internal/httpapi/httpapi_test.go`:
```go
import "github.com/iot-root/garden-of-eden/internal/config"

func TestSchedulePutGetAndEnable(t *testing.T) {
	st := state.New()
	c := core.New(mock.New(), st)
	go c.Run()
	defer c.Stop()

	store := config.Schedules{}
	deps := ControlDeps{
		GetSchedules: func() config.Schedules { return store },
		PutSchedule: func(ch string, s config.Schedule) error {
			if ch == "light" {
				store.Light = s
			} else {
				store.Pump = s
			}
			return nil
		},
		SetScheduleEnabled: func(ch string, on bool) error {
			if ch == "light" {
				store.Light.Enabled = on
			}
			return nil
		},
		SetWaterLowCM: func(cm float64) error { return nil },
	}
	h := HandlerFull(c, st, mock.New(), state.NewFrames(), deps)

	body := `{"enabled":true,"entries":[{"at":"06:00","action":"on","brightness":70}]}`
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/schedules/light", strings.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT status = %d body=%s", rec.Code, rec.Body.String())
	}
	if !store.Light.Enabled || len(store.Light.Entries) != 1 {
		t.Errorf("schedule not stored: %+v", store.Light)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/schedules", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "06:00") {
		t.Errorf("GET schedules = %d %s", rec.Code, rec.Body.String())
	}
}

func TestWaterThresholdEndpoint(t *testing.T) {
	st := state.New()
	c := core.New(mock.New(), st)
	go c.Run()
	defer c.Stop()
	var gotCM float64
	deps := ControlDeps{
		GetSchedules:       func() config.Schedules { return config.Schedules{} },
		PutSchedule:        func(string, config.Schedule) error { return nil },
		SetScheduleEnabled: func(string, bool) error { return nil },
		SetWaterLowCM:      func(cm float64) error { gotCM = cm; return nil },
	}
	h := HandlerFull(c, st, mock.New(), state.NewFrames(), deps)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/water/low-threshold", strings.NewReader(`{"cm":8.5}`)))
	if rec.Code != http.StatusOK || gotCM != 8.5 {
		t.Errorf("threshold endpoint: code=%d gotCM=%v", rec.Code, gotCM)
	}
}

func TestCameraEndpoint(t *testing.T) {
	st := state.New()
	c := core.New(mock.New(), st)
	go c.Run()
	defer c.Stop()
	frames := state.NewFrames()
	frames.SetUpper([]byte{0xFF, 0xD8, 0xFF, 0xD9})
	deps := ControlDeps{
		GetSchedules: func() config.Schedules { return config.Schedules{} },
		PutSchedule:  func(string, config.Schedule) error { return nil },
		SetScheduleEnabled: func(string, bool) error { return nil },
		SetWaterLowCM: func(float64) error { return nil },
	}
	h := HandlerFull(c, st, mock.New(), frames, deps)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/camera/upper.jpg", nil))
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "image/jpeg" {
		t.Errorf("camera endpoint: code=%d ct=%s", rec.Code, rec.Header().Get("Content-Type"))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/httpapi/ -run 'Schedule|Water|Camera' -v`
Expected: build failure — `undefined: ControlDeps`/`HandlerFull`.

- [ ] **Step 3: Implement the routes**

In `internal/httpapi/httpapi.go` add (building on Plan 2's `sensorMux`):
```go
import (
	"github.com/iot-root/garden-of-eden/internal/config"
	"github.com/iot-root/garden-of-eden/internal/hw"
	"github.com/iot-root/garden-of-eden/internal/state"
)

type ControlDeps struct {
	GetSchedules       func() config.Schedules
	PutSchedule        func(channel string, s config.Schedule) error
	SetScheduleEnabled func(channel string, enabled bool) error
	SetWaterLowCM      func(cm float64) error
}

// HandlerFull is the complete API: base control + sensor reads + schedules +
// water + cameras.
func HandlerFull(c *core.Core, st *state.Store, d hw.Devices, frames *state.Frames, deps ControlDeps) http.Handler {
	mux := sensorMux(c, st, d) // Plan 2 returns *http.ServeMux (base + sensor GET routes)

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
		for _, e := range s.Entries {
			if e.Action != "on" && e.Action != "off" {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "action must be on|off"})
				return
			}
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
			_, _ = w.Write(b)
		}
	}
	mux.HandleFunc("GET /camera/upper.jpg", serveFrame(frames.Upper))
	mux.HandleFunc("GET /camera/lower.jpg", serveFrame(frames.Lower))

	return mux
}
```

> Plan 2 dependency: ensure Plan 2's sensor handler is refactored to expose
> `func sensorMux(c *core.Core, st *state.Store, d hw.Devices) *http.ServeMux`
> that starts from Plan 1's `baseMux(c, st)` and adds the sensor GET routes.
> `HandlerFull` builds on it.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/httpapi/ -v`
Expected: PASS.

---

### Task 7: Optional zeroconf advertiser

**Depends on:** Plan 1 main

**Files:**
- Create: `internal/discovery/discovery.go`

Advertises `_gardynd._tcp` on the LAN so the HA integration can auto-discover the
service. Best-effort: failure to advertise never blocks startup.

- [ ] **Step 1: Implement the advertiser**

`internal/discovery/discovery.go`:
```go
// Package discovery advertises gardynd via mDNS/zeroconf (_gardynd._tcp).
package discovery

import (
	"log"

	"github.com/grandcat/zeroconf"
)

// Advertise registers the service. Returns a shutdown func; both are no-ops on
// error (best-effort).
func Advertise(instance string, port int) func() {
	server, err := zeroconf.Register(instance, "_gardynd._tcp", "local.", port, []string{"path=/state"}, nil)
	if err != nil {
		log.Printf("zeroconf advertise failed (continuing): %v", err)
		return func() {}
	}
	return server.Shutdown
}
```

- [ ] **Step 2: Add dependency, build**

Run: `go get github.com/grandcat/zeroconf@latest && go build ./internal/discovery/`
Expected: builds.

---

### Task 8: Wire everything in main, full suite, commit

**Depends on:** Tasks 3–7

**Files:**
- Modify: `cmd/gardynd/main.go`

- [ ] **Step 1: Wire scheduler, publishers, control deps, config persistence**

After building `core`/`store`/`devs` (and only for `--hw=real`/`mock`):
```go
	c.SetWaterLowCM(cfg.Water.LowCM)
	c.SetPumpMaxRuntime(10 * time.Minute)
	c.SetCutLightOnOverTemp(cfg.OverTemp.CutLight)

	var schedMu sync.Mutex
	schedules := cfg.Schedules
	st.SetScheduleEnabled("light", schedules.Light.Enabled)
	st.SetScheduleEnabled("pump", schedules.Pump.Enabled)

	getSchedules := func() config.Schedules { schedMu.Lock(); defer schedMu.Unlock(); return schedules }
	persist := func() error {
		if *configPath == "" {
			return nil
		}
		schedMu.Lock(); cfg.Schedules = schedules; snapshot := cfg; schedMu.Unlock()
		return snapshot.Save(*configPath)
	}
	putSchedule := func(ch string, s config.Schedule) error {
		schedMu.Lock()
		if ch == "light" { schedules.Light = s } else { schedules.Pump = s }
		schedMu.Unlock()
		st.SetScheduleEnabled(ch, s.Enabled)
		return persist()
	}
	setSchedEnabled := func(ch string, on bool) error {
		schedMu.Lock()
		if ch == "light" { schedules.Light.Enabled = on } else { schedules.Pump.Enabled = on }
		schedMu.Unlock()
		return persist()
	}
	setWaterCM := func(cm float64) error {
		c.SetWaterLowCM(cm)
		schedMu.Lock(); cfg.Water.LowCM = cm; schedMu.Unlock()
		return persist()
	}

	sched := core.NewScheduler(c, getSchedules)
	go sched.Run()
	defer sched.Stop()

	frames := state.NewFrames()
	pub := publish.New(devs, st, frames, 30*time.Minute)
	go pub.Run()
	go pub.RunCameras(time.Duration(cfg.Camera.IntervalSeconds) * time.Second)
	defer pub.Stop()

	if devs.Button != nil {
		go func() {
			for ev := range devs.Button.Events() {
				switch ev {
				case hw.SinglePress:
					c.Submit(core.Command{Target: core.TargetLight, Action: core.ActionOn})
				case hw.DoublePress:
					c.Submit(core.Command{Target: core.TargetPump, Action: core.ActionOn})
				}
			}
		}()
	}

	stop := discovery.Advertise("gardynd-"+cfg.Device.Identifier, cfg.HTTP.Port)
	defer stop()

	deps := httpapi.ControlDeps{
		GetSchedules: getSchedules, PutSchedule: putSchedule,
		SetScheduleEnabled: setSchedEnabled, SetWaterLowCM: setWaterCM,
	}
	server := &http.Server{Addr: addr, Handler: httpapi.HandlerFull(c, st, devs, frames, deps)}
```
Add imports: `sync`, `time`, `internal/publish`, `internal/discovery`, `internal/hw`.
Ensure `config.CameraConfig.IntervalSeconds` (default 3600, env
`IMAGE_INTERVAL_SECONDS`) exists from Plan 2; if not, add it.

> Design note: the Python button *toggled*. On-press here turns on; exact toggle
> parity can be a small follow-up (add `ActionToggle` to core). The spec did not
> require toggle semantics.

- [ ] **Step 2: Build and run full suite**

Run: `make tidy && make build && make build-pi && go test ./... -race`
Expected: all PASS, both binaries build, no races.

- [ ] **Step 3: Mock end-to-end check**

Run `./bin/gardynd --hw=mock --config /tmp/g.yaml` (seed `/tmp/g.yaml` with a
light schedule whose "on" time is in the past). Confirm: `GET /state` shows the
light on (catch-up) and sensor values populated; `PUT /schedules/light` updates
`/tmp/g.yaml`; `POST /water/low-threshold {"cm":8}` updates `state.water`;
`GET /camera/upper.jpg` returns JPEG bytes.

- [ ] **Step 4: Commit (single commit)**

Run:
```
git add internal/ cmd/ go.mod go.sum
git commit -m "feat: scheduler, water-low interlock, pump failsafe, over-temp, sensor/camera publishers, schedule+water REST, zeroconf"
```

---

## Self-Review

**Spec coverage:** state-based scheduler + restart catch-up ✓; midnight wrap ✓;
per-channel enable/disable via REST ✓; schedule CRUD persisted atomically ✓;
water-low interlock across REST/button/scheduler (all funnel through `applyPump`)
✓; flash-lights warning ✓; pump max-runtime failsafe ✓; over-temp → snapshot +
optional cut-light ✓; periodic sensor reads into store ✓; camera capture + JPEG
serving ✓; water threshold REST ✓; zeroconf ✓; no MQTT ✓. The HA integration
consumes `/state` + `/schedules` + `/schedule/{ch}/enabled` + `/water/low-threshold`
+ `/camera/*.jpg`.

**Placeholder scan:** Only the button-toggle and Plan-2 `sensorMux` notes, both
concrete (deferred-with-instructions / cross-plan contract), not "TBD".

**Type consistency:** `config.Schedule/ScheduleEntry/Schedules`,
`Schedule.StateAt/DueBetween`, `core.NewScheduler(*Core, func() config.Schedules)`,
`core.SetWaterLowCM/SetPumpMaxRuntime/SetCutLightOnOverTemp`,
`state.Frames` (`SetUpper/Upper/...`), `state.PumpPower`,
`publish.New(hw.Devices, *state.Store, *state.Frames, time.Duration)`,
`httpapi.ControlDeps{GetSchedules, PutSchedule, SetScheduleEnabled, SetWaterLowCM}`
+ `HandlerFull` are used identically across tasks and main. Core writes to the
store via the Plan 1 setters (`SetPump`, `SetWater`, `SetOverTemp`, `SetLight`).

**Dependency audit:** Task 1 (none) pure. Task 2 → Task 1. Task 3 → Task 2 + Plan
2 sensors. Task 4 → Tasks 1+3. Task 5 creates `state/frames.go` + `internal/publish`
(depends on Plan 2 sensors + Task 6? No — Task 5 defines frames; Task 6 consumes
frames). Corrected order: Task 5 defines `state.Frames` and the publisher; Task 6
(httpapi) depends on Task 5 for `*state.Frames`. Task 6 also depends on Tasks 1+2
and Plan 2's `sensorMux`. Task 7 independent (`internal/discovery`). Task 8
modifies `main.go`, depends on 3–7. Only Task 1 and Task 7 are dependency-free
and they touch disjoint packages. Audit clean.
