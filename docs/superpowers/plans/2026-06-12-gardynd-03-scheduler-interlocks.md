# gardynd Plan 3 — Scheduler, Interlocks & Publishers Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add the on-device behavior that makes gardynd a complete replacement: a state-based scheduler with restart catch-up, the water-low interlock enforced across every pump-on path, a pump max-runtime failsafe, the over-temp monitor, periodic sensor + camera publishers, and the remaining Home Assistant discovery entities.

**Architecture:** Behavior that touches device state stays in the single-writer `core`: the scheduler, interlock, and failsafe submit/observe through it. Publishers are independent goroutines that read sensors and emit `core.StateChange`-style MQTT messages. Schedule config persists atomically to the YAML file and is editable over REST.

**Tech Stack:** Go stdlib `time`, `gopkg.in/yaml.v3` (atomic save), existing core/mqtt/httpapi packages.

**Spec:** `docs/superpowers/specs/2026-06-12-gardynd-go-service-design.md`
**Depends on:** Plan 1 (core, mqtt, httpapi, config) and Plan 2 (sensor interfaces on `hw.Devices`).

---

## File Structure (this plan)

```
internal/config/config.go         MODIFY: schedule + water + overtemp config + atomic Save
internal/config/schedule.go       schedule types + timeline evaluation (pure)
internal/config/schedule_test.go
internal/core/core.go             MODIFY: water-low interlock, pump failsafe, overtemp, schedule hooks
internal/core/core_test.go        MODIFY: interlock + failsafe tests
internal/core/scheduler.go        scheduler goroutine (uses timeline eval)
internal/core/scheduler_test.go
internal/mqttsvc/discovery.go     MODIFY: sensor/water/camera/schedule-switch discovery
internal/mqttsvc/discovery_test.go MODIFY
internal/mqttsvc/mqttsvc.go       MODIFY: schedule-switch + water/low command handling
internal/publish/publish.go       periodic sensor + camera publishers
internal/publish/publish_test.go
internal/httpapi/httpapi.go       MODIFY: /schedules CRUD
internal/httpapi/httpapi_test.go  MODIFY
cmd/gardynd/main.go               MODIFY: start scheduler + publishers
```

---

### Task 1: Schedule types + timeline evaluation (pure, unit-tested)

**Depends on:** none (within this plan)

**Files:**
- Create: `internal/config/schedule.go`
- Test: `internal/config/schedule_test.go`

The core insight from the spec: scheduling is **state-based**. Given a list of
`{at, action, brightness}` entries and the current wall-clock time, compute the
state the timeline implies *right now* (the most recent past entry, wrapping
across midnight). This is what enables restart catch-up and is fully testable.

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
	// Before the first entry of the day, state comes from the LAST entry
	// (previous evening), wrapping midnight.
	s := lightSched()
	st, _ := s.StateAt(mins(2, 0)) // 02:00, before 06:00
	if st.On {
		t.Errorf("02:00 => %+v, want off (carried from 20:00 off)", st)
	}
}

func TestDueEntries(t *testing.T) {
	s := lightSched()
	// Entries that fire when crossing from 05:59 to 06:00.
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
	At         string `yaml:"at"`     // "HH:MM"
	Action     string `yaml:"action"` // "on" | "off"
	Brightness int    `yaml:"brightness,omitempty"`
}

type Schedule struct {
	Enabled bool            `yaml:"enabled"`
	Entries []ScheduleEntry `yaml:"entries"`
}

type Schedules struct {
	Light Schedule `yaml:"light"`
	Pump  Schedule `yaml:"pump"`
}

// ChannelState is the on/off (+brightness) state a timeline implies.
type ChannelState struct {
	On         bool
	Brightness int
}

// minutes parses "HH:MM" to minutes-since-midnight.
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

// sortedEntries returns entries with parsed minute marks, ascending.
func (s Schedule) sortedEntries() []struct {
	min   int
	entry ScheduleEntry
} {
	type pe = struct {
		min   int
		entry ScheduleEntry
	}
	var out []pe
	for _, e := range s.Entries {
		if m, err := minutes(e.At); err == nil {
			out = append(out, pe{m, e})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].min < out[j].min })
	return out
}

// StateAt returns the channel state implied at nowMin (minutes since midnight).
// ok=false when there are no valid entries.
func (s Schedule) StateAt(nowMin int) (ChannelState, bool) {
	es := s.sortedEntries()
	if len(es) == 0 {
		return ChannelState{}, false
	}
	// Most recent entry at or before nowMin; if none today, wrap to the last.
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

// DueBetween returns entries whose time is in (prevMin, nowMin], handling the
// normal forward case (no midnight wrap within a single tick).
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

In `internal/config/config.go`, add fields to `Config`:
```go
	Schedules Schedules     `yaml:"schedules"`
	Water     WaterConfig   `yaml:"water"`
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
In `applyEnv`, preserve the existing env knob:
```go
	if v, ok := os.LookupEnv("WATER_LOW_CM"); ok && v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			c.Water.LowCM = f
		}
	}
```
Add the atomic save (write temp file in same dir, then rename):
```go
import (
	// add:
	"path/filepath"
)

// Save writes the config to path atomically (temp file + rename).
func (c Config) Save(path string) error {
	data, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".config-*.yaml")
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

### Task 3: Core — water-low interlock, pump failsafe, over-temp

**Depends on:** Task 2, Plan 2 Task 1 (`hw.Devices` sensor fields)

**Files:**
- Modify: `internal/core/core.go`
- Test: `internal/core/core_test.go` (append)

The interlock must guard **every** pump-on (MQTT, REST, button, scheduler) —
this is the single-writer's job. We also add a max-runtime failsafe and the
over-temp reaction.

- [ ] **Step 1: Write the failing tests (append)**

Append to `internal/core/core_test.go`:
```go
import (
	"github.com/iot-root/garden-of-eden/internal/config"
	mockhw "github.com/iot-root/garden-of-eden/internal/hw/mock"
)

func TestPumpBlockedWhenWaterLow(t *testing.T) {
	devs := mockhw.New()
	devs.Distance.(*mockhw.Distance).CM = 12.0 // > threshold => too low
	c := New(devs)
	c.SetWaterLowCM(10.0)
	events := c.Subscribe()
	go c.Run()
	defer c.Stop()

	c.Submit(Command{Target: TargetPump, Action: ActionOn})

	got := drain(events, 2, time.Second)
	var pumpOn, waterLow bool
	for _, s := range got {
		if s.Topic == "pump/state" && s.Payload == "ON" {
			pumpOn = true
		}
		if s.Topic == "water/low/state" && s.Payload == "ON" {
			waterLow = true
		}
	}
	if pumpOn {
		t.Error("pump turned on despite low water")
	}
	if !waterLow {
		t.Error("water/low/state ON not published")
	}
	if devs.Pump.(*mockhw.Pump).Speed() != 0 {
		t.Error("pump hardware was driven")
	}
}

func TestPumpAllowedWhenWaterOK(t *testing.T) {
	devs := mockhw.New()
	devs.Distance.(*mockhw.Distance).CM = 5.0 // <= threshold => ok
	c := New(devs)
	c.SetWaterLowCM(10.0)
	events := c.Subscribe()
	go c.Run()
	defer c.Stop()

	c.Submit(Command{Target: TargetPump, Action: ActionOn})
	got := drain(events, 2, time.Second)
	on := false
	for _, s := range got {
		if s.Topic == "pump/state" && s.Payload == "ON" {
			on = true
		}
	}
	if !on {
		t.Error("pump should have turned on")
	}
}

func TestPumpFailsafeForcesOff(t *testing.T) {
	devs := mockhw.New()
	c := New(devs)
	c.SetPumpMaxRuntime(50 * time.Millisecond)
	events := c.Subscribe()
	go c.Run()
	defer c.Stop()

	c.Submit(Command{Target: TargetPump, Action: ActionOn})
	// Within ~50ms the failsafe should publish pump/state OFF.
	deadline := time.After(time.Second)
	for {
		select {
		case s := <-events:
			if s.Topic == "pump/state" && s.Payload == "OFF" {
				return // success
			}
		case <-deadline:
			t.Fatal("failsafe did not turn pump off")
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/core/ -run 'Water|Failsafe' -v`
Expected: build failure — `SetWaterLowCM`/`SetPumpMaxRuntime` undefined.

- [ ] **Step 3: Implement in core**

In `internal/core/core.go`, extend the `Core` struct and `New`:
```go
import "time"

// add fields:
//   waterLowCM     float64
//   pumpMaxRuntime time.Duration
//   pumpTimer      *time.Timer

func (c *Core) SetWaterLowCM(cm float64)            { c.waterLowCM = cm }
func (c *Core) SetPumpMaxRuntime(d time.Duration)   { c.pumpMaxRuntime = d }
```
Add a config setter used by main/REST:
```go
// WaterLowEnabled reports whether the interlock is active.
func (c *Core) waterLowEnabled() bool { return c.waterLowCM > 0 }
```
Rewrite `applyPump`'s `ActionOn` branch to enforce the interlock and arm the
failsafe (replace the existing case):
```go
	case ActionOn:
		if c.waterLowEnabled() {
			cm, err := c.measureDistance()
			if err == nil && cm > c.waterLowCM {
				// Too low: abort, warn, flash lights.
				c.emit("water/low/state", "ON")
				c.flashLights()
				return
			}
			c.emit("water/low/state", "OFF")
		}
		if cmd.Value > 0 {
			c.pumpLevel = cmd.Value
		}
		if err := c.dev.Pump.SetSpeed(c.pumpLevel); err != nil {
			log.Printf("pump on: %v", err)
			return
		}
		c.armPumpFailsafe()
		c.emit("pump/state", "ON")
		c.emit("pump/speed/state", strconv.Itoa(c.pumpLevel))
```
In `applyPump`'s `ActionOff`, disarm the failsafe before emitting:
```go
	case ActionOff:
		c.disarmPumpFailsafe()
		if err := c.dev.Pump.Off(); err != nil {
			log.Printf("pump off: %v", err)
			return
		}
		c.emit("pump/state", "OFF")
```
Add the helpers (all run on the core goroutine, so no extra locking):
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
Add `"fmt"` to the imports. Over-temp handling: add a command the monitor
submits and have core react.
```go
const TargetOverTemp Target = 2

// in apply():
//   case TargetOverTemp: c.applyOverTemp(cmd) // cmd.Value: 1=alert,0=clear

func (c *Core) applyOverTemp(cmd Command) {
	alert := cmd.Value == 1
	if alert {
		c.emit("overtemp/state", "ON")
		if c.cutLightOnOverTemp && c.dev.Light != nil {
			_ = c.dev.Light.Off()
			c.emit("light/state", "OFF")
		}
	} else {
		c.emit("overtemp/state", "OFF")
	}
}
```
Add `cutLightOnOverTemp bool` field + `func (c *Core) SetCutLightOnOverTemp(b bool) { c.cutLightOnOverTemp = b }`.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/core/ -v`
Expected: PASS (note: `flashLights` makes the low-water test take ~1.8s — keep
`drain`'s timeout ≥2s; bump the test timeout if needed).

> Efficiency note: `flashLights` blocks the core goroutine ~1.8s. That is
> acceptable (no pump-on should proceed during a low-water warning) and matches
> the Python behavior. Document it; do not background it, or the interlock would
> race.

---

### Task 4: Scheduler goroutine

**Depends on:** Task 3 (core setters + interlock), Task 1 (timeline eval)

**Files:**
- Create: `internal/core/scheduler.go`
- Test: `internal/core/scheduler_test.go`

The scheduler does two things: on start, apply the current implied state
(catch-up); then each minute, submit commands for entries that just became due.
Per-channel `enabled` gates it.

- [ ] **Step 1: Write the failing test**

`internal/core/scheduler_test.go`:
```go
package core

import (
	"testing"
	"time"

	"github.com/iot-root/garden-of-eden/internal/config"
	mockhw "github.com/iot-root/garden-of-eden/internal/hw/mock"
)

func TestSchedulerCatchUpAppliesCurrentState(t *testing.T) {
	devs := mockhw.New()
	c := New(devs)
	go c.Run()
	defer c.Stop()

	sched := config.Schedule{Enabled: true, Entries: []config.ScheduleEntry{
		{At: "06:00", Action: "on", Brightness: 70},
		{At: "20:00", Action: "off"},
	}}
	s := NewScheduler(c, func() config.Schedules {
		return config.Schedules{Light: sched}
	})
	// Pretend "now" is 12:00 — between on and off => light should be ON@70.
	s.CatchUpAt(12 * 60)

	if !waitInt(func() int { return devs.Light.(*mockhw.Light).Brightness() }, 70) {
		t.Errorf("catch-up did not set brightness to 70")
	}
}

func TestSchedulerSkipsDisabledChannel(t *testing.T) {
	devs := mockhw.New()
	c := New(devs)
	go c.Run()
	defer c.Stop()
	sched := config.Schedule{Enabled: false, Entries: []config.ScheduleEntry{
		{At: "06:00", Action: "on", Brightness: 70},
	}}
	s := NewScheduler(c, func() config.Schedules { return config.Schedules{Light: sched} })
	s.CatchUpAt(12 * 60)

	time.Sleep(100 * time.Millisecond)
	if devs.Light.(*mockhw.Light).Brightness() != 0 {
		t.Error("disabled schedule should not drive the light")
	}
}

func waitInt(get func() int, want int) bool {
	for i := 0; i < 50; i++ {
		if get() == want {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
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

// Scheduler applies schedule timelines to the core. schedFn returns the current
// schedules (read fresh each tick so REST edits take effect live).
type Scheduler struct {
	core    *Core
	schedFn func() config.Schedules
	done    chan struct{}
}

func NewScheduler(c *Core, schedFn func() config.Schedules) *Scheduler {
	return &Scheduler{core: c, schedFn: schedFn, done: make(chan struct{})}
}

func nowMinutes(t time.Time) int { return t.Hour()*60 + t.Minute() }

// CatchUpAt applies the implied state for both channels at nowMin.
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

### Task 5: Periodic sensor + camera publishers

**Depends on:** Plan 2 Task 1 (sensors), Plan 1 mqtt forwarding

**Files:**
- Create: `internal/publish/publish.go`
- Test: `internal/publish/publish_test.go`

Publishers read sensors on an interval and emit relative topics through a sink
(the same `core.StateChange` shape the MQTT layer already forwards). Decoupling
via a sink interface keeps them unit-testable.

- [ ] **Step 1: Write the failing test**

`internal/publish/publish_test.go`:
```go
package publish

import (
	"sync"
	"testing"
	"time"

	"github.com/iot-root/garden-of-eden/internal/hw/mock"
)

type capture struct {
	mu sync.Mutex
	m  map[string]string
}

func (c *capture) Publish(topic, payload string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil {
		c.m = map[string]string{}
	}
	c.m[topic] = payload
}
func (c *capture) get(t string) string { c.mu.Lock(); defer c.mu.Unlock(); return c.m[t] }

func TestPublishOnceEmitsSensors(t *testing.T) {
	devs := mock.New()
	cap := &capture{}
	p := New(devs, cap, time.Hour)
	p.publishOnce()

	for _, topic := range []string{"temperature", "humidity", "pcb/temperature", "water/level"} {
		if cap.get(topic) == "" {
			t.Errorf("missing publish for %q", topic)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/publish/ -v`
Expected: build failure — `undefined: New`.

- [ ] **Step 3: Implement the publishers**

`internal/publish/publish.go`:
```go
// Package publish runs periodic sensor and camera reads and emits MQTT-relative
// topics through a Sink.
package publish

import (
	"encoding/base64"
	"fmt"
	"log"
	"time"

	"github.com/iot-root/garden-of-eden/internal/hw"
)

// Sink receives device-relative topic/payload pairs (the MQTT layer prefixes
// the base topic).
type Sink interface {
	Publish(topic, payload string)
}

type Publisher struct {
	dev      hw.Devices
	sink     Sink
	interval time.Duration
	done     chan struct{}
}

func New(dev hw.Devices, sink Sink, interval time.Duration) *Publisher {
	return &Publisher{dev: dev, sink: sink, interval: interval, done: make(chan struct{})}
}

func (p *Publisher) publishOnce() {
	if p.dev.Env != nil {
		if t, h, err := p.dev.Env.Read(); err == nil {
			p.sink.Publish("temperature", fmt.Sprintf("%.2f", t))
			p.sink.Publish("humidity", fmt.Sprintf("%.2f", h))
		} else {
			log.Printf("env read: %v", err)
		}
	}
	if p.dev.PCBTemp != nil {
		if t, err := p.dev.PCBTemp.Temperature(); err == nil {
			p.sink.Publish("pcb/temperature", fmt.Sprintf("%.2f", t))
		}
	}
	if p.dev.Distance != nil {
		if cm, err := p.dev.Distance.MeasureCM(); err == nil {
			p.sink.Publish("water/level", fmt.Sprintf("%.2f", cm))
		}
	}
	if p.dev.Power != nil {
		if r, err := p.dev.Power.Read(); err == nil {
			p.sink.Publish("pump/power/voltage", fmt.Sprintf("%.2f", r.BusVoltage))
			p.sink.Publish("pump/power/current", fmt.Sprintf("%.2f", r.Current))
			p.sink.Publish("pump/power/watts", fmt.Sprintf("%.2f", r.Power))
		}
	}
}

// Run publishes immediately, then every interval.
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

func (p *Publisher) Stop() { close(p.done) }

// RunCameras publishes base64 JPEGs on its own (slower) interval.
func (p *Publisher) RunCameras(interval time.Duration) {
	tick := func() {
		p.captureAndPublish(p.dev.UpperCamera, "image/upper_camera")
		p.captureAndPublish(p.dev.LowerCamera, "image/lower_camera")
	}
	tick()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-p.done:
			return
		case <-ticker.C:
			tick()
		}
	}
}

func (p *Publisher) captureAndPublish(cam hw.Camera, topic string) {
	if cam == nil {
		return
	}
	jpeg, err := cam.Capture()
	if err != nil {
		log.Printf("camera %s: %v", topic, err)
		return
	}
	p.sink.Publish(topic, base64.StdEncoding.EncodeToString(jpeg))
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/publish/ -v`
Expected: PASS.

> Note: the original published raw JPEG bytes with `encoding: b64` discovery.
> Here we base64-encode in the publisher and keep `encoding: b64` in discovery
> (Task 6), so the payload is a valid UTF-8 string through the existing string
> sink. Equivalent end result in Home Assistant.

---

### Task 6: Discovery for sensors, water, cameras, schedule switches

**Depends on:** Plan 1 discovery, Task 1 (schedule types)

**Files:**
- Modify: `internal/mqttsvc/discovery.go`
- Test: `internal/mqttsvc/discovery_test.go` (append)

Add the remaining HA entities, preserving the Python topics/`unique_id`s:
PCB temp, temperature, humidity, water level, water-low binary sensor, water-low
threshold number, water-low mode sensor, over-temp binary sensor, two camera
image entities, and two schedule-enable switches (new).

- [ ] **Step 1: Write the failing test (append)**

Append to `internal/mqttsvc/discovery_test.go`:
```go
func TestExtendedDiscoveryEntities(t *testing.T) {
	msgs := DiscoveryMessages(dev())
	want := []string{
		"homeassistant/sensor/gardyn/gardyn-xx_pcb_temp/config",
		"homeassistant/sensor/gardyn/gardyn-xx_temperature/config",
		"homeassistant/sensor/gardyn/gardyn-xx_humidity/config",
		"homeassistant/sensor/gardyn/gardyn-xx_water_level/config",
		"homeassistant/binary_sensor/gardyn/gardyn-xx_water_low/config",
		"homeassistant/number/gardyn/gardyn-xx_water_low_cm/config",
		"homeassistant/image/gardyn/gardyn-xx_upper_camera/config",
		"homeassistant/switch/gardyn/gardyn-xx_light_schedule/config",
		"homeassistant/switch/gardyn/gardyn-xx_pump_schedule/config",
		"homeassistant/binary_sensor/gardyn/gardyn-xx_overtemp/config",
	}
	for _, topic := range want {
		if _, ok := msgs[topic]; !ok {
			t.Errorf("missing discovery topic %q", topic)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/mqttsvc/ -run Extended -v`
Expected: FAIL — topics missing.

- [ ] **Step 3: Extend DiscoveryMessages**

In `internal/mqttsvc/discovery.go`, after the light/pump entries in
`DiscoveryMessages`, add (preserving Python field names exactly; all include
`"device": info` and `"availability_topic": avail`):
```go
	id := d.Identifier
	sensor := func(slug, name, stateTopic, unit, deviceClass string) {
		p := map[string]any{
			"name": name, "unique_id": id + "_" + slug,
			"state_topic": stateTopic, "availability_topic": avail, "device": info,
		}
		if unit != "" {
			p["unit_of_measurement"] = unit
		}
		if deviceClass != "" {
			p["device_class"] = deviceClass
		}
		out["homeassistant/sensor/gardyn/"+id+"_"+slug+"/config"] = mustJSON(p)
	}
	sensor("pcb_temp", "PCB Temperature", base+"/pcb/temperature", "°C", "temperature")
	sensor("temperature", "Temperature", base+"/temperature", "°C", "temperature")
	sensor("humidity", "Humidity", base+"/humidity", "%", "humidity")
	sensor("water_level", "Water Level", base+"/water/level", "cm", "distance")
	sensor("water_low_mode", "Water Low Mode", base+"/water/low/mode", "", "")

	out["homeassistant/binary_sensor/gardyn/"+id+"_water_low/config"] = mustJSON(map[string]any{
		"name": "Water Low", "unique_id": id + "_water_low", "platform": "mqtt",
		"state_topic": base + "/water/low/state", "device_class": "problem",
		"payload_on": "ON", "payload_off": "OFF",
		"availability_topic": avail, "device": info,
	})
	out["homeassistant/number/gardyn/"+id+"_water_low_cm/config"] = mustJSON(map[string]any{
		"name": "Set Water Low Threshold", "unique_id": id + "_water_low_cm", "platform": "mqtt",
		"state_topic": base + "/water/low/cm", "command_topic": base + "/water/low/cm/set",
		"min": 0, "max": 15, "step": 0.5, "unit_of_measurement": "cm", "device_class": "distance",
		"availability_topic": avail, "device": info,
	})
	out["homeassistant/binary_sensor/gardyn/"+id+"_overtemp/config"] = mustJSON(map[string]any{
		"name": "Over Temperature", "unique_id": id + "_overtemp", "platform": "mqtt",
		"state_topic": base + "/overtemp/state", "device_class": "problem",
		"payload_on": "ON", "payload_off": "OFF",
		"availability_topic": avail, "device": info,
	})

	image := func(slug, name, topic string) {
		out["homeassistant/image/gardyn/"+id+"_"+slug+"/config"] = mustJSON(map[string]any{
			"name": name, "unique_id": id + "_" + slug, "image_topic": topic,
			"encoding": "b64", "content_type": "image/jpeg", "object_id": id + "_" + slug,
			"availability_topic": avail, "device": info,
		})
	}
	image("upper_camera", "Upper Camera", base+"/image/upper_camera")
	image("lower_camera", "Lower Camera", base+"/image/lower_camera")

	schedSwitch := func(slug, name, stateTopic, cmdTopic string) {
		out["homeassistant/switch/gardyn/"+id+"_"+slug+"/config"] = mustJSON(map[string]any{
			"name": name, "unique_id": id + "_" + slug, "platform": "mqtt",
			"state_topic": stateTopic, "command_topic": cmdTopic,
			"payload_on": "ON", "payload_off": "OFF",
			"availability_topic": avail, "device": info,
		})
	}
	schedSwitch("light_schedule", "Light Schedule", base+"/schedule/light/state", base+"/schedule/light/set")
	schedSwitch("pump_schedule", "Pump Schedule", base+"/schedule/pump/state", base+"/schedule/pump/set")
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/mqttsvc/ -v`
Expected: PASS.

---

### Task 7: MQTT command handling for water threshold + schedule switches

**Depends on:** Task 3 (core setters), Task 6 (topics), Task 2 (config Save)

**Files:**
- Modify: `internal/mqttsvc/mqttsvc.go`
- Test: `internal/mqttsvc/mqttsvc_test.go` (append)

Handle the new inbound command topics: `water/low/cm/set` (updates the
interlock threshold + persists), and `schedule/{light,pump}/set` (enable/disable
+ persists + re-publishes switch state). These need callbacks into core/config.

- [ ] **Step 1: Extend the Service constructor signature**

Change `mqttsvc.New` to accept a small hooks struct so MQTT can mutate core and
persist config without importing httpapi:
```go
type Hooks struct {
	SetWaterLowCM   func(cm float64)
	SetScheduleOn   func(channel string, on bool) // channel: "light"|"pump"
	WaterLowCM      func() float64
	ScheduleEnabled func(channel string) bool
}

// New(cfg, core, hooks) — thread hooks through to onMessage/onConnect.
```

- [ ] **Step 2: Write the failing test (append)**

Append to `internal/mqttsvc/mqttsvc_test.go`:
```go
func TestWaterThresholdCommandUpdatesHook(t *testing.T) {
	addr := startBroker(t)
	probe, got := subClient(t, addr)
	defer probe.Disconnect(100)

	var gotCM float64
	hooks := Hooks{
		SetWaterLowCM:   func(cm float64) { gotCM = cm },
		WaterLowCM:      func() float64 { return gotCM },
		SetScheduleOn:   func(string, bool) {},
		ScheduleEnabled: func(string) bool { return true },
	}
	cfg := config.Config{Device: config.DeviceConfig{BaseTopic: "gardyn", Identifier: "gardyn-xx"}}
	host, port, _ := net.SplitHostPort(addr)
	cfg.MQTT.Broker, cfg.MQTT.Port = host, atoiHelper(port)

	c := core.New(mock.New())
	go c.Run()
	defer c.Stop()
	svc, err := New(cfg, c, hooks)
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Stop()

	probe.Publish("gardyn/water/low/cm/set", 0, false, "8.5")
	deadline := time.After(time.Second)
	for gotCM != 8.5 {
		select {
		case <-deadline:
			t.Fatalf("threshold not updated, got %v", gotCM)
		case <-time.After(20 * time.Millisecond):
		}
	}
	_ = got
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/mqttsvc/ -run WaterThreshold -v`
Expected: build failure — `New` signature mismatch / `Hooks` undefined.

- [ ] **Step 4: Implement handling**

Add `hooks Hooks` to `Service`; in `onMessage`, before the core-command mapping,
handle the new topics:
```go
	switch suffix {
	case "water/low/cm/set":
		if f, err := strconv.ParseFloat(payload, 64); err == nil {
			s.hooks.SetWaterLowCM(f)
			s.client.Publish(s.base+"/water/low/cm", 0, true, fmt.Sprintf("%.2f", f))
			mode := "Disabled"
			if f > 0 {
				mode = "Enabled"
			}
			s.client.Publish(s.base+"/water/low/mode", 0, true, mode)
		}
		return
	case "schedule/light/set", "schedule/pump/set":
		ch := strings.Split(suffix, "/")[1]
		on := strings.EqualFold(payload, "ON")
		s.hooks.SetScheduleOn(ch, on)
		s.client.Publish(s.base+"/schedule/"+ch+"/state", 0, true, stateStr(on))
		return
	}
```
Add `import "fmt"` and:
```go
func stateStr(on bool) string {
	if on {
		return "ON"
	}
	return "OFF"
}
```
In `onConnect`, after discovery, publish initial schedule switch + water states:
```go
	for _, ch := range []string{"light", "pump"} {
		client.Publish(s.base+"/schedule/"+ch+"/state", 0, true, stateStr(s.hooks.ScheduleEnabled(ch)))
	}
	cm := s.hooks.WaterLowCM()
	client.Publish(s.base+"/water/low/cm", 0, true, fmt.Sprintf("%.2f", cm))
	mode := "Disabled"
	if cm > 0 {
		mode = "Enabled"
	}
	client.Publish(s.base+"/water/low/mode", 0, true, mode)
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/mqttsvc/ -v`
Expected: PASS.

---

### Task 8: REST `/schedules` CRUD

**Depends on:** Task 1 (schedule types), Task 2 (Save)

**Files:**
- Modify: `internal/httpapi/httpapi.go`
- Test: `internal/httpapi/httpapi_test.go` (append)

`GET /schedules` returns current schedules; `PUT /schedules/{channel}` replaces
one channel and persists. The handler takes getter/setter callbacks so httpapi
stays decoupled from config persistence.

- [ ] **Step 1: Write the failing test (append)**

Append to `internal/httpapi/httpapi_test.go`:
```go
func TestSchedulePutAndGet(t *testing.T) {
	c := core.New(mock.New())
	go c.Run()
	defer c.Stop()

	store := config.Schedules{}
	deps := ScheduleDeps{
		Get: func() config.Schedules { return store },
		Put: func(ch string, s config.Schedule) error {
			if ch == "light" {
				store.Light = s
			} else {
				store.Pump = s
			}
			return nil
		},
	}
	h := HandlerWithSchedules(c, mock.New(), deps)

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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/httpapi/ -run Schedule -v`
Expected: build failure — `undefined: ScheduleDeps`.

- [ ] **Step 3: Implement the routes**

In `internal/httpapi/httpapi.go`:
```go
import "github.com/iot-root/garden-of-eden/internal/config"

type ScheduleDeps struct {
	Get func() config.Schedules
	Put func(channel string, s config.Schedule) error
}

func HandlerWithSchedules(c *core.Core, d hw.Devices, sd ScheduleDeps) http.Handler {
	mux := sensorMux(c, d) // factored from HandlerWithSensors, returns *http.ServeMux

	mux.HandleFunc("GET /schedules", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, sd.Get())
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
		if err := sd.Put(ch, s); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "saved"})
	})
	return mux
}
```
Refactor: extract the sensor-route body from `HandlerWithSensors` into
`func sensorMux(c *core.Core, d hw.Devices) *http.ServeMux` and have
`HandlerWithSensors` return it, so both share the base.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/httpapi/ -v`
Expected: PASS.

---

### Task 9: Wire everything in main, full suite, commit

**Depends on:** Tasks 3–8

**Files:**
- Modify: `cmd/gardynd/main.go`

- [ ] **Step 1: Wire scheduler, publishers, hooks, config persistence**

In `cmd/gardynd/main.go` after building `core` and before blocking on signal:
```go
	// Apply persisted settings to core.
	c.SetWaterLowCM(cfg.Water.LowCM)
	c.SetPumpMaxRuntime(10 * time.Minute)
	c.SetCutLightOnOverTemp(cfg.OverTemp.CutLight)

	// Live schedule store, guarded for REST edits.
	var schedMu sync.Mutex
	schedules := cfg.Schedules
	getSchedules := func() config.Schedules { schedMu.Lock(); defer schedMu.Unlock(); return schedules }
	putSchedule := func(ch string, s config.Schedule) error {
		schedMu.Lock()
		if ch == "light" {
			schedules.Light = s
		} else {
			schedules.Pump = s
		}
		cfg.Schedules = schedules
		schedMu.Unlock()
		if *configPath != "" {
			return cfg.Save(*configPath)
		}
		return nil
	}

	sched := core.NewScheduler(c, getSchedules)
	go sched.Run()
	defer sched.Stop()

	hooks := mqttsvc.Hooks{
		SetWaterLowCM: func(cm float64) {
			c.SetWaterLowCM(cm)
			schedMu.Lock(); cfg.Water.LowCM = cm; schedMu.Unlock()
			if *configPath != "" { _ = cfg.Save(*configPath) }
		},
		WaterLowCM:      func() float64 { return cfg.Water.LowCM },
		SetScheduleOn:   func(ch string, on bool) {
			schedMu.Lock()
			if ch == "light" { schedules.Light.Enabled = on } else { schedules.Pump.Enabled = on }
			cfg.Schedules = schedules
			schedMu.Unlock()
			if *configPath != "" { _ = cfg.Save(*configPath) }
		},
		ScheduleEnabled: func(ch string) bool {
			schedMu.Lock(); defer schedMu.Unlock()
			if ch == "light" { return schedules.Light.Enabled }
			return schedules.Pump.Enabled
		},
	}

	// MQTT sink adapter for publishers.
	pub := publish.New(devs, mqttSink{svc}, 30*time.Minute)
	go pub.Run()
	go pub.RunCameras(time.Duration(cfg.Camera.IntervalSeconds) * time.Second)
	defer pub.Stop()
```
Update the `mqttsvc.New` call to pass `hooks`; update the HTTP handler to
`httpapi.HandlerWithSchedules(c, devs, httpapi.ScheduleDeps{Get: getSchedules, Put: putSchedule})`.
Add a thin sink that forwards to MQTT — add an exported method on the MQTT
service and the adapter:
```go
// in mqttsvc: func (s *Service) PublishRelative(topic, payload string) { s.client.Publish(s.base+"/"+topic, 0, false, payload) }

type mqttSink struct{ svc *mqttsvc.Service }
func (m mqttSink) Publish(topic, payload string) { m.svc.PublishRelative(topic, payload) }
```
Add `IntervalSeconds int yaml:"interval_seconds"` to `config.CameraConfig`
(default 3600, env `IMAGE_INTERVAL_SECONDS`) in Plan 2's config — if not
present, add it here. Add imports: `sync`, `time`, `internal/publish`.

- [ ] **Step 2: Wire the button gestures (single=light, double=pump)**

Add after the scheduler start:
```go
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
```
(`SinglePress` toggling is acceptable as on-press; full toggle parity can track
current state via core in a later refinement — out of scope here.)

> Design note: the Python button *toggled*. To match exactly, add `ActionToggle`
> to core; deferred as a small follow-up since on-press is functionally close and
> the spec did not call out toggle semantics. If exact parity is required, add
> the toggle action in this task.

- [ ] **Step 3: Build and run full suite**

Run: `make tidy && make build && make build-pi && go test ./...`
Expected: all PASS, both binaries build.

- [ ] **Step 4: Mock end-to-end check**

Run `./bin/gardynd --hw=mock --config /tmp/g.yaml` (seed `/tmp/g.yaml` with a
light schedule whose "on" time is in the past). Confirm via `mosquitto_sub`:
catch-up turns the light on at start, `gardyn/schedule/light/state` is `ON`,
`PUT /schedules/light` updates `/tmp/g.yaml`, and `water/low/cm/set` updates the
threshold + mode.

- [ ] **Step 5: Commit (single commit)**

Run:
```
git add internal/ cmd/ go.mod go.sum
git commit -m "feat: scheduler, water-low interlock, pump failsafe, over-temp, sensor/camera publishers, schedule API"
```

---

## Self-Review

**Spec coverage:** state-based scheduler + restart catch-up ✓; midnight wrap ✓;
per-channel enable/disable via MQTT switch + REST ✓; schedule CRUD persisted
atomically ✓; water-low interlock across MQTT/REST/button/scheduler (enforced in
core, all paths funnel through `applyPump`) ✓; flash-lights warning ✓; pump
max-runtime failsafe ✓; over-temp binary_sensor + optional cut-light ✓; periodic
sensor publishers (temp/humidity/pcb/water/power) ✓; camera publishing + image
discovery ✓; availability retained on all new entities ✓. The HA custom
integration remains the separate follow-up (consumes `/schedules` + schedule
switches defined here).

**Placeholder scan:** Two explicit design notes (button toggle semantics; camera
b64 encoding) describe concrete, in-scope-or-deferred decisions, not unfinished
code. No "TBD"/"handle edge cases" placeholders.

**Type consistency:** `config.Schedule{Enabled, Entries}`,
`config.ScheduleEntry{At, Action, Brightness}`, `Schedule.StateAt(int)
(ChannelState,bool)`, `Schedule.DueBetween(int,int)`, `core.NewScheduler(*Core,
func() config.Schedules)`, `core.SetWaterLowCM/SetPumpMaxRuntime/SetCutLightOnOverTemp`,
`mqttsvc.Hooks{SetWaterLowCM, SetScheduleOn, WaterLowCM, ScheduleEnabled}`,
`mqttsvc.Service.PublishRelative`, `publish.New(hw.Devices, Sink, time.Duration)`,
`httpapi.ScheduleDeps{Get, Put}` / `HandlerWithSchedules` are used identically
across tasks and main. Relative publish topics (`temperature`, `water/level`,
`overtemp/state`, `schedule/light/state`, …) match the discovery `state_topic`s
once the MQTT layer prepends `<base>/`.

**Dependency audit:** Task 1 (none) is pure. Task 2 depends on Task 1 (schedule
types in config). Task 3 depends on Task 2 + Plan 2 sensors. Task 4 depends on
Tasks 1+3. Task 5 depends on Plan 2 (sensors) only — disjoint package
(`internal/publish`), parallelizable with Task 6. Task 6 depends on Task 1
(schedule existence only for switch topics — actually uses just `DeviceConfig`;
safe). Task 7 depends on Tasks 3+6 (shares `mqttsvc` with Task 6 → serialized).
Task 8 depends on Tasks 1+2. Task 9 modifies `main.go` and depends on 3–8. No
`Depends on: none` task (only Task 1) shares files with another. Audit clean.
