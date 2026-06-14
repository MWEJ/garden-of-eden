# gardynd Plan 10 — Ops & Real-Time (DX) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add config hot-reload (mtime-poll, stdlib only), real-time push via SSE (`GET /state/stream`), and CI hardening (golangci-lint, mock end-to-end smoke job, ruff lint for Python HA integration).

**Architecture:** Hot-reload lives in a thin goroutine in `main.go` that calls a pure `reloadDiff` helper (unit-tested in isolation); the helper takes old and new file-only configs and applies only the live-mutable fields through existing closures/setters. SSE push adds a `Subscribe`/`notify` mechanism directly to `state.Store` (buffered size-1 channels, non-blocking coalescing sends), then a `GET /state/stream` handler registered inside `HandlerFull`; no new packages, no SSE libraries. CI hardening adds three new jobs to the existing single-job workflow: `lint` (golangci-lint), `e2e-mock` (binary smoke test), and `ruff` (Python guard). All existing steps are preserved.

**Tech Stack:** Go stdlib only (`net/http`, `sync`, `os`, `time`; `http.Flusher` for SSE). No fsnotify. No SSE libraries. `golangci/golangci-lint-action@v6` for linting. `ruff` for Python. `gopkg.in/yaml.v3` already present (no new Go deps).

**Spec:** `docs/superpowers/specs/2026-06-12-gardynd-go-service-design.md`
**Depends on:** Plan 1 (core, state store, httpapi, config), Plan 2 (sensor interfaces, `sensorMux`), Plan 3 (publisher, scheduler, `HandlerFull`, `ControlDeps`), Plan 6 (state freshness: `SetDeviceInfo`, `TelemetryIntervalSeconds`).

---

## File Structure (this plan)

```
internal/config/reload.go           NEW: pure reloadDiff helper (no I/O, unit-testable)
internal/config/reload_test.go      NEW: unit tests for reloadDiff
internal/state/state.go             MODIFY: add subscribers map + notify(); Subscribe(); all mutating setters call notify()
internal/state/state_test.go        MODIFY: append Subscribe/notify/cancel unit tests
internal/httpapi/httpapi.go         MODIFY: add GET /state/stream SSE handler registered inside HandlerFull
internal/httpapi/httpapi_test.go    MODIFY: append SSE handler test
cmd/gardynd/main.go                 MODIFY: add reloader goroutine wired to reloadDiff + existing closures; add configReloadConst
.github/workflows/build.yml         MODIFY: add lint job, e2e-mock job, ruff job
```

---

### Task 1: Config — pure `reloadDiff` helper + unit tests

**Depends on:** none (within this plan; touches only `internal/config/`)

**Files:**
- Create: `internal/config/reload.go`
- Create: `internal/config/reload_test.go`

The pure helper `ApplyReload` receives the old and new `Config` values (both from `LoadFileOnly`; never from `Load` so env/flag overrides are excluded) and calls a set of setter callbacks only for the fields that differ. HTTP port, bind address, and all interval fields require a restart and are intentionally NOT reapplied — this is documented in the function's godoc and tested with a no-op callback that would panic if called.

The design choice for `ConfigReloadSeconds`: **hardcode a constant `configReloadInterval = 5 * time.Second`** in `reload.go` and document it there. Rationale: adding a YAML field for a polling interval that itself cannot be hot-reloaded creates confusion (changing the field requires a restart anyway). A 5-second poll is fast enough for DX and cheap enough on a Pi Zero; callers that need a different value can pass the constant through a flag at startup (future plan concern, not YAGNI here).

- [ ] **Step 1: Write the failing tests**

Create `internal/config/reload_test.go`:
```go
package config

import "testing"

// reloadCallbacks captures which callbacks were called and with what values.
type reloadCallbacks struct {
	scheduleAssigns  []string        // "light" or "pump", order recorded
	lightSchedule    Schedule
	pumpSchedule     Schedule
	lightEnabled     *bool
	pumpEnabled      *bool
	waterLowCM       *float64
	cutLightOnOverTemp *bool
}

func (cb *reloadCallbacks) opts() ReloadOpts {
	return ReloadOpts{
		SetLightSchedule: func(s Schedule) {
			cb.scheduleAssigns = append(cb.scheduleAssigns, "light")
			cb.lightSchedule = s
		},
		SetPumpSchedule: func(s Schedule) {
			cb.scheduleAssigns = append(cb.scheduleAssigns, "pump")
			cb.pumpSchedule = s
		},
		SetLightEnabled: func(on bool) { cb.lightEnabled = &on },
		SetPumpEnabled:  func(on bool) { cb.pumpEnabled = &on },
		SetWaterLowCM:   func(cm float64) { cb.waterLowCM = &cm },
		SetCutLightOnOverTemp: func(b bool) { cb.cutLightOnOverTemp = &b },
	}
}

func baseConfig() Config {
	c := defaults()
	c.Schedules.Light = Schedule{Enabled: true, Entries: []ScheduleEntry{{At: "06:00", Action: "on", Brightness: 70}}}
	c.Water.LowCM = 10.0
	c.OverTemp.CutLight = false
	return c
}

func TestApplyReloadScheduleChanged(t *testing.T) {
	old := baseConfig()
	newCfg := baseConfig()
	newCfg.Schedules.Light.Entries[0].Brightness = 80

	cb := &reloadCallbacks{}
	ApplyReload(old, newCfg, cb.opts())

	if len(cb.scheduleAssigns) != 1 || cb.scheduleAssigns[0] != "light" {
		t.Errorf("expected light schedule callback, got %v", cb.scheduleAssigns)
	}
	if cb.lightSchedule.Entries[0].Brightness != 80 {
		t.Errorf("brightness = %d, want 80", cb.lightSchedule.Entries[0].Brightness)
	}
	if cb.lightEnabled == nil || *cb.lightEnabled != true {
		t.Errorf("SetLightEnabled not called with true; got %v", cb.lightEnabled)
	}
}

func TestApplyReloadPumpEnabledChanged(t *testing.T) {
	old := baseConfig()
	newCfg := baseConfig()
	newCfg.Schedules.Pump.Enabled = true // was false in baseConfig defaults

	cb := &reloadCallbacks{}
	ApplyReload(old, newCfg, cb.opts())

	// pump schedule changed (enabled flag flipped) → both schedule + enabled callbacks
	if cb.pumpEnabled == nil || *cb.pumpEnabled != true {
		t.Errorf("SetPumpEnabled not called with true; got %v", cb.pumpEnabled)
	}
}

func TestApplyReloadWaterLowCMChanged(t *testing.T) {
	old := baseConfig()
	newCfg := baseConfig()
	newCfg.Water.LowCM = 15.0

	cb := &reloadCallbacks{}
	ApplyReload(old, newCfg, cb.opts())

	if cb.waterLowCM == nil || *cb.waterLowCM != 15.0 {
		t.Errorf("SetWaterLowCM not called with 15.0; got %v", cb.waterLowCM)
	}
}

func TestApplyReloadOverTempChanged(t *testing.T) {
	old := baseConfig()
	newCfg := baseConfig()
	newCfg.OverTemp.CutLight = true

	cb := &reloadCallbacks{}
	ApplyReload(old, newCfg, cb.opts())

	if cb.cutLightOnOverTemp == nil || *cb.cutLightOnOverTemp != true {
		t.Errorf("SetCutLightOnOverTemp not called with true; got %v", cb.cutLightOnOverTemp)
	}
}

func TestApplyReloadHTTPPortIgnored(t *testing.T) {
	// HTTP port must NOT trigger any callback — it requires a restart.
	old := baseConfig()
	newCfg := baseConfig()
	newCfg.HTTP.Port = 9999 // changed — must be ignored

	panicIfCalled := func(name string) func() {
		return func() { t.Errorf("callback %s called but HTTP port change must be ignored", name) }
	}
	_ = panicIfCalled // used implicitly: none of the below callbacks should fire

	cb := &reloadCallbacks{}
	ApplyReload(old, newCfg, cb.opts())

	// No schedule, water, or overtemp change → no callbacks at all.
	if len(cb.scheduleAssigns) != 0 {
		t.Errorf("unexpected schedule callback: %v", cb.scheduleAssigns)
	}
	if cb.waterLowCM != nil {
		t.Errorf("unexpected water callback: %v", *cb.waterLowCM)
	}
	if cb.cutLightOnOverTemp != nil {
		t.Errorf("unexpected overtemp callback: %v", *cb.cutLightOnOverTemp)
	}
}

func TestApplyReloadNoChangeNoCallback(t *testing.T) {
	old := baseConfig()
	newCfg := baseConfig() // identical

	cb := &reloadCallbacks{}
	ApplyReload(old, newCfg, cb.opts())

	if len(cb.scheduleAssigns) != 0 || cb.waterLowCM != nil || cb.cutLightOnOverTemp != nil {
		t.Errorf("no change should produce no callbacks; got assigns=%v water=%v overtemp=%v",
			cb.scheduleAssigns, cb.waterLowCM, cb.cutLightOnOverTemp)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config/ -run 'TestApplyReload' -v`
Expected: build failure — `undefined: ApplyReload`, `undefined: ReloadOpts`.

- [ ] **Step 3: Implement `reload.go`**

Create `internal/config/reload.go`:
```go
// Package config — reload.go: pure helper for config hot-reload.
//
// configReloadInterval is the mtime-poll cadence used by the reloader goroutine
// in main. It is a constant rather than a config field because the reload interval
// cannot itself be hot-reloaded — changing it would require a restart — and
// hard-coding 5 s is fast enough for DX while cheap on a Pi Zero.
package config

import (
	"reflect"
	"time"
)

// ConfigReloadInterval is the mtime-poll cadence for the hot-reload goroutine.
// Exposed as a package-level constant so main can use it without duplication.
const ConfigReloadInterval = 5 * time.Second

// ReloadOpts holds injectable setter callbacks for every live-mutable field.
// Fields that require a restart (HTTP.Port, HTTP.Bind, all *IntervalSeconds)
// have no slot here — they are intentionally absent.
type ReloadOpts struct {
	// Schedule callbacks: called when the corresponding schedule differs.
	// The full new Schedule value is delivered; callers must also assign it
	// into their schedMu-guarded copy and call SetScheduleEnabled on the store.
	SetLightSchedule      func(s Schedule)
	SetPumpSchedule       func(s Schedule)
	SetLightEnabled       func(on bool)
	SetPumpEnabled        func(on bool)
	// Water interlock threshold.
	SetWaterLowCM         func(cm float64)
	// Over-temperature: whether to cut the light on alert.
	SetCutLightOnOverTemp func(b bool)
}

// ApplyReload compares oldCfg and newCfg (both from LoadFileOnly, never from
// Load, so env/flag overrides are excluded) and calls the ReloadOpts callbacks
// for every live-mutable field that changed. It is pure (no I/O, no goroutines)
// and is the correct unit to test.
//
// NOT hot-reloaded (require a restart): HTTP.Port, Camera.*IntervalSeconds,
// TelemetryIntervalSeconds. Document this to operators in the config YAML.
func ApplyReload(oldCfg, newCfg Config, opts ReloadOpts) {
	// Schedules: compare with reflect.DeepEqual to catch entry-level changes.
	if !reflect.DeepEqual(oldCfg.Schedules.Light, newCfg.Schedules.Light) {
		if opts.SetLightSchedule != nil {
			opts.SetLightSchedule(newCfg.Schedules.Light)
		}
		if opts.SetLightEnabled != nil {
			opts.SetLightEnabled(newCfg.Schedules.Light.Enabled)
		}
	}
	if !reflect.DeepEqual(oldCfg.Schedules.Pump, newCfg.Schedules.Pump) {
		if opts.SetPumpSchedule != nil {
			opts.SetPumpSchedule(newCfg.Schedules.Pump)
		}
		if opts.SetPumpEnabled != nil {
			opts.SetPumpEnabled(newCfg.Schedules.Pump.Enabled)
		}
	}
	// Water interlock threshold.
	if oldCfg.Water.LowCM != newCfg.Water.LowCM {
		if opts.SetWaterLowCM != nil {
			opts.SetWaterLowCM(newCfg.Water.LowCM)
		}
	}
	// Over-temp cut-light.
	if oldCfg.OverTemp.CutLight != newCfg.OverTemp.CutLight {
		if opts.SetCutLightOnOverTemp != nil {
			opts.SetCutLightOnOverTemp(newCfg.OverTemp.CutLight)
		}
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/config/ -run 'TestApplyReload' -v`
Expected:
```
--- PASS: TestApplyReloadScheduleChanged (0.00s)
--- PASS: TestApplyReloadPumpEnabledChanged (0.00s)
--- PASS: TestApplyReloadWaterLowCMChanged (0.00s)
--- PASS: TestApplyReloadOverTempChanged (0.00s)
--- PASS: TestApplyReloadHTTPPortIgnored (0.00s)
--- PASS: TestApplyReloadNoChangeNoCallback (0.00s)
PASS
```

- [ ] **Step 5: Run full config suite to confirm no regressions**

Run: `go test ./internal/config/ -v`
Expected: all existing tests PASS plus the six new ones.

---

### Task 2: Main — mtime-poll reloader goroutine wired to `ApplyReload` + existing closures

**Depends on:** Task 1 (`config.ApplyReload`, `config.ConfigReloadInterval`)

**Files:**
- Modify: `cmd/gardynd/main.go`

The reloader goroutine is added in `main.go` after all closures (`putSchedule`, `setSchedEnabled`, `setWaterCM`) are defined. It polls `os.Stat(*configPath).ModTime()` every `config.ConfigReloadInterval`; when mtime advances it calls `config.LoadFileOnly` and passes old+new to `config.ApplyReload` with a `ReloadOpts` that delegates to the already-existing main closures and `c.SetCutLightOnOverTemp`. It keeps `fileCfg` (already present as the "file-only baseline") as the "last loaded" snapshot and updates it after each reload. If `*configPath == ""` (no config file supplied), the goroutine exits immediately. A structured log line is emitted on each reload.

This goroutine is thin by design: all testable logic is in `config.ApplyReload` (Task 1). The goroutine itself is not unit-tested — its surface is the mtime check and goroutine lifecycle, which are I/O-bound.

- [ ] **Step 1: Add the reloader goroutine to `cmd/gardynd/main.go`**

In `cmd/gardynd/main.go`, locate the block after `setWaterCM` is defined (around line 120) and BEFORE `sched := core.NewScheduler(...)`. Insert the following block. The `fileCfg` variable is already declared and populated earlier in main (around line 42); the reloader closes over it.

```go
	// Hot-reload: poll mtime every configReloadInterval, reapply live-mutable
	// fields. Disabled when no config file is supplied (--config="").
	// NOT hot-reloaded: HTTP port/bind, *IntervalSeconds — those require restart.
	if *configPath != "" {
		go func() {
			ticker := time.NewTicker(config.ConfigReloadInterval)
			defer ticker.Stop()
			// lastMod is the mtime at startup.
			info, err := os.Stat(*configPath)
			if err != nil {
				log.Printf("config reload: initial stat failed: %v", err)
				return
			}
			lastMod := info.ModTime()
			for range ticker.C {
				info, err := os.Stat(*configPath)
				if err != nil {
					log.Printf("config reload: stat: %v", err)
					continue
				}
				if !info.ModTime().After(lastMod) {
					continue
				}
				newFileCfg, err := config.LoadFileOnly(*configPath)
				if err != nil {
					log.Printf("config reload: parse error (keeping old config): %v", err)
					continue
				}
				config.ApplyReload(fileCfg, newFileCfg, config.ReloadOpts{
					SetLightSchedule: func(s config.Schedule) {
						schedMu.Lock()
						schedules.Light = s
						schedMu.Unlock()
					},
					SetPumpSchedule: func(s config.Schedule) {
						schedMu.Lock()
						schedules.Pump = s
						schedMu.Unlock()
					},
					SetLightEnabled: func(on bool) {
						st.SetScheduleEnabled("light", on)
					},
					SetPumpEnabled: func(on bool) {
						st.SetScheduleEnabled("pump", on)
					},
					SetWaterLowCM: func(cm float64) {
						c.SetWaterLowCM(cm)
						schedMu.Lock()
						fileCfg.Water.LowCM = cm
						schedMu.Unlock()
					},
					SetCutLightOnOverTemp: func(b bool) {
						c.SetCutLightOnOverTemp(b)
					},
				})
				fileCfg = newFileCfg
				lastMod = info.ModTime()
				log.Printf("config reloaded from %s", *configPath)
			}
		}()
	}
```

Ensure `"os"` is already imported in `main.go` (it is — used for `os.Signal`). No new imports needed.

- [ ] **Step 2: Build to confirm it compiles**

Run: `make build`
Expected: `bin/gardynd` built with no errors.

- [ ] **Step 3: Smoke-test the reload path manually**

Start the binary, edit the config file's `water.low_cm`, wait 6 seconds, and confirm the log line appears:
```
./bin/gardynd --hw=mock --config /tmp/g-reload.yaml &
sleep 1
python3 -c "import yaml,time; d=yaml.safe_load(open('/tmp/g-reload.yaml')); d.setdefault('water', {})['low_cm']=12.5; open('/tmp/g-reload.yaml','w').write(yaml.dump(d))"
sleep 6
# check logs — should show: config reloaded from /tmp/g-reload.yaml
kill %1
```
Expected: the log line `config reloaded from /tmp/g-reload.yaml` appears within ~6 seconds of the file change.

---

### Task 3: State store — `Subscribe`/`notify`/`cancel` mechanism + unit tests

**Depends on:** none (within this plan; touches only `internal/state/`)

**Files:**
- Modify: `internal/state/state.go`
- Modify: `internal/state/state_test.go`

Add a subscriber registry to `state.Store`. Every mutating setter calls `notify()` after releasing (or while holding) the write lock — we call it while holding the lock to keep things simple and consistent. `Subscribe()` returns a buffered (capacity 1) `<-chan struct{}` and a cancel function; `notify()` does a non-blocking send to all registered channels (drop if full — this is intentional coalescing: the consumer sees "something changed", reads the latest snapshot, and doesn't need intermediate states). The `subscribers` map is guarded by the same `sync.RWMutex` (`mu`) — upgraded to a write lock during `Subscribe`/`cancel` and during `notify`.

**Design note on locking:** `notify()` is called with `mu` held (write lock already held by the setter). The non-blocking send `select { case ch <- struct{}{}: default: }` is safe with the mutex held because the channel operations never block and no subscriber callback holds `mu`. This avoids a second mutex.

- [ ] **Step 1: Write the failing tests (append to `internal/state/state_test.go`)**

Append to `internal/state/state_test.go`:
```go
import (
	"sync"
	"time"
)

func TestSubscribeReceivesNotifyOnSetLight(t *testing.T) {
	s := New()
	ch, cancel := s.Subscribe()
	defer cancel()

	s.SetLight(true, 80)

	select {
	case <-ch:
		// good — notification received
	case <-time.After(100 * time.Millisecond):
		t.Fatal("SetLight did not notify subscriber within 100ms")
	}
}

func TestSubscribeCancelUnregisters(t *testing.T) {
	s := New()
	ch, cancel := s.Subscribe()
	cancel()

	s.SetLight(true, 80)

	// After cancel the channel must not receive a new notification.
	select {
	case <-ch:
		t.Fatal("cancelled subscriber should not receive notifications")
	case <-time.After(50 * time.Millisecond):
		// good — silence after cancel
	}
}

func TestSubscribeFullChannelDoesNotBlockSetter(t *testing.T) {
	s := New()
	ch, cancel := s.Subscribe()
	defer cancel()

	// Fill the channel without draining it.
	s.SetLight(true, 50)  // first notify — fills the buffer
	s.SetPump(true, 100)  // second notify — channel full, must NOT block

	// The setter returned promptly (no deadlock). Drain once.
	select {
	case <-ch:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("channel should have had exactly one notification buffered")
	}
}

func TestMultipleSubscribers(t *testing.T) {
	s := New()
	ch1, cancel1 := s.Subscribe()
	ch2, cancel2 := s.Subscribe()
	defer cancel1()
	defer cancel2()

	s.SetPump(false, 0)

	var wg sync.WaitGroup
	wg.Add(2)
	recv := func(ch <-chan struct{}, name string) {
		defer wg.Done()
		select {
		case <-ch:
		case <-time.After(100 * time.Millisecond):
			t.Errorf("%s: did not receive notification", name)
		}
	}
	go recv(ch1, "sub1")
	go recv(ch2, "sub2")
	wg.Wait()
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/state/ -run 'TestSubscribe|TestMultiple' -v`
Expected: build failure — `s.Subscribe undefined`.

- [ ] **Step 3: Add `Subscribe`/`notify` to `internal/state/state.go`**

In `internal/state/state.go`, extend `Store` with a subscriber registry and `nextID` counter. Add `notify()` to each mutating setter. Full diff follows — show the complete modified struct and all changed setters:

```go
// Package state holds the thread-safe device snapshot served by GET /state.
package state

import (
	"sync"
	"time"
)

// ... existing type declarations (LightState, PumpState, etc.) unchanged ...

type Store struct {
	mu          sync.RWMutex
	start       time.Time
	snap        Snapshot
	// SSE push: each Subscribe() allocates a buffered chan and registers it here.
	// notify() is called inside every mutating setter (while mu write-lock held).
	nextSubID   uint64
	subscribers map[uint64]chan struct{}
}

func New() *Store {
	return &Store{
		start: time.Now(),
		snap: Snapshot{
			Available: true,
			Schedules: map[string]SchedFlag{"light": {}, "pump": {}},
		},
		subscribers: make(map[uint64]chan struct{}),
	}
}
```

Add the `Subscribe` method and `notify` helper immediately after `New()`:

```go
// Subscribe returns a buffered (capacity 1) channel that receives an empty
// struct whenever any setter mutates the snapshot. The caller must call cancel
// when done to free resources. The channel is never closed by the store.
//
// Notification is coalescing: if the subscriber has not drained the channel
// before the next mutation, the extra send is dropped (non-blocking). The
// subscriber should always read the latest Snapshot() rather than counting
// notifications.
func (s *Store) Subscribe() (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	s.mu.Lock()
	id := s.nextSubID
	s.nextSubID++
	s.subscribers[id] = ch
	s.mu.Unlock()
	cancel := func() {
		s.mu.Lock()
		delete(s.subscribers, id)
		s.mu.Unlock()
	}
	return ch, cancel
}

// notify sends a non-blocking signal to all subscribers. Must be called with
// s.mu write-lock held (all setters hold it, so this is always satisfied).
func (s *Store) notify() {
	for _, ch := range s.subscribers {
		select {
		case ch <- struct{}{}:
		default:
			// channel full — drop; subscriber will read the latest snapshot on next drain
		}
	}
}
```

Now add `s.notify()` as the last statement (before `s.mu.Unlock()`) in every mutating setter. The complete updated setters:

```go
func (s *Store) SetLight(on bool, brightness int) {
	s.mu.Lock()
	s.snap.Light = LightState{On: on, Brightness: brightness}
	s.notify()
	s.mu.Unlock()
}

func (s *Store) SetPump(on bool, speed int) {
	s.mu.Lock()
	s.snap.Pump = PumpState{On: on, Speed: speed}
	s.notify()
	s.mu.Unlock()
}

func (s *Store) SetWater(thresholdCM float64, low bool) {
	s.mu.Lock()
	s.snap.Water = WaterState{LowThresholdCM: thresholdCM, Low: low}
	s.notify()
	s.mu.Unlock()
}

func (s *Store) SetOverTemp(v bool) {
	s.mu.Lock()
	s.snap.OverTemp = v
	s.notify()
	s.mu.Unlock()
}

func (s *Store) SetScheduleEnabled(channel string, enabled bool) {
	s.mu.Lock()
	s.snap.Schedules[channel] = SchedFlag{Enabled: enabled}
	s.notify()
	s.mu.Unlock()
}

func (s *Store) SetTemperature(c float64) {
	s.mu.Lock()
	s.snap.Sensors.TemperatureC = &c
	s.notify()
	s.mu.Unlock()
}

func (s *Store) SetHumidity(p float64) {
	s.mu.Lock()
	s.snap.Sensors.HumidityPct = &p
	s.notify()
	s.mu.Unlock()
}

func (s *Store) SetPCBTemp(c float64) {
	s.mu.Lock()
	s.snap.Sensors.PCBTempC = &c
	s.notify()
	s.mu.Unlock()
}

func (s *Store) SetWaterLevel(cm float64) {
	s.mu.Lock()
	s.snap.Sensors.WaterLevelCM = &cm
	s.notify()
	s.mu.Unlock()
}

func (s *Store) SetPumpPower(p PumpPower) {
	s.mu.Lock()
	s.snap.Sensors.Pump = &p
	s.notify()
	s.mu.Unlock()
}
```

If `SetDeviceInfo` was added by Plan 6, add `s.notify()` there too:
```go
func (s *Store) SetDeviceInfo(identifier, model, version string) {
	s.mu.Lock()
	s.snap.Identifier = identifier
	s.snap.Model = model
	s.snap.Version = version
	s.notify()
	s.mu.Unlock()
}
```

- [ ] **Step 4: Run new tests to verify they pass**

Run: `go test ./internal/state/ -run 'TestSubscribe|TestMultiple' -v`
Expected:
```
--- PASS: TestSubscribeReceivesNotifyOnSetLight (0.00s)
--- PASS: TestSubscribeCancelUnregisters (0.00s)
--- PASS: TestSubscribeFullChannelDoesNotBlockSetter (0.00s)
--- PASS: TestMultipleSubscribers (0.00s)
PASS
```

- [ ] **Step 5: Run full state suite with race detector to confirm no regressions**

Run: `go test ./internal/state/ -race -v`
Expected: all existing tests PASS plus the four new ones, no data races.

---

### Task 4: httpapi — `GET /state/stream` SSE handler + test

**Depends on:** Task 3 (state.Store `Subscribe`/`notify`)

**Files:**
- Modify: `internal/httpapi/httpapi.go`
- Modify: `internal/httpapi/httpapi_test.go`

Add the SSE handler as a named function `streamHandler` and register it on the mux inside `HandlerFull`. The handler:
1. Sets SSE headers (`Content-Type: text/event-stream`, `Cache-Control: no-cache`, `X-Accel-Buffering: no`).
2. Asserts `http.Flusher`; returns 501 if not supported (rare, always log).
3. Calls `st.Subscribe()` and defers `cancel()`.
4. Sends the current snapshot immediately as `data: <json>\n\n` and flushes.
5. Starts a periodic heartbeat ticker (30 s) for NAT/proxy keepalive.
6. Selects on: subscriber channel (send latest snapshot frame), heartbeat (send SSE comment `:\n\n`), `r.Context().Done()` (client disconnect — return cleanly).

The SSE route is registered inside `HandlerFull` (not in `baseMux` or `sensorMux`) to keep it co-located with the other full-feature routes and avoid re-plumbing function signatures.

**Test approach:** Use `httptest.NewRecorder` with a context that we cancel. Since `httptest.ResponseRecorder` does not implement `http.Flusher`, we need a thin wrapper. Use a `flushRecorder` struct embedding `httptest.ResponseRecorder` with a no-op `Flush()` method so the assertion inside the handler succeeds. The test starts the handler in a goroutine, triggers a state change (`st.SetLight(true, 99)`), waits for the response body to contain at least one `data:` line, then cancels the context.

- [ ] **Step 1: Write the failing test (append to `internal/httpapi/httpapi_test.go`)**

Append to `internal/httpapi/httpapi_test.go`:
```go
import (
	"bufio"
	"bytes"
	"context"
	"io"
	"net/http/httptest"
)

// flushRecorder wraps httptest.ResponseRecorder with a no-op Flush so the SSE
// handler's http.Flusher assertion succeeds in tests.
type flushRecorder struct {
	*httptest.ResponseRecorder
}

func (f *flushRecorder) Flush() { f.ResponseRecorder.Flush() }

func TestSSEStreamDeliversDataFrame(t *testing.T) {
	st := state.New()
	c := core.New(mock.New(), st)
	go c.Run()
	defer c.Stop()

	deps := ControlDeps{
		GetSchedules:       func() config.Schedules { return config.Schedules{} },
		PutSchedule:        func(string, config.Schedule) error { return nil },
		SetScheduleEnabled: func(string, bool) error { return nil },
		SetWaterLowCM:      func(float64) error { return nil },
	}
	h := HandlerFull(c, st, mock.New(), state.NewFrames(), deps)

	ctx, cancelCtx := context.WithCancel(context.Background())
	defer cancelCtx()

	pr, pw := io.Pipe()
	rec := httptest.NewRecorder()
	// We need a writer that goes to the pipe so we can read lines from the handler.
	// Use a custom ResponseWriter backed by the pipe.
	type pipeWriter struct {
		http.ResponseWriter
		pw *io.PipeWriter
	}
	// Instead: use httptest.Server so we get a real connection and real Flusher.
	// This avoids the ResponseRecorder Flusher issue entirely.
	_ = rec
	_ = pr
	_ = pw

	srv := httptest.NewServer(h)
	defer srv.Close()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/state/stream", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("SSE status = %d, want 200", resp.StatusCode)
	}
	ct := resp.Header.Get("Content-Type")
	if ct != "text/event-stream" {
		t.Fatalf("Content-Type = %q, want text/event-stream", ct)
	}

	// Read lines from the stream in a goroutine; cancel context when we find data:.
	found := make(chan bool, 1)
	go func() {
		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			line := scanner.Text()
			if bytes.HasPrefix([]byte(line), []byte("data:")) {
				found <- true
				return
			}
		}
		found <- false
	}()

	// Trigger a state change to guarantee a second data: frame arrives.
	time.Sleep(20 * time.Millisecond) // let handler send the initial frame first
	st.SetLight(true, 99)

	select {
	case ok := <-found:
		if !ok {
			t.Fatal("SSE stream closed without any data: line")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no data: frame received within 2s")
	}
	cancelCtx()
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/httpapi/ -run 'TestSSEStream' -v`
Expected: FAIL — `undefined: bufio` / handler returns 404 for `/state/stream` (route not yet registered).

- [ ] **Step 3: Implement `streamHandler` and register it in `HandlerFull`**

In `internal/httpapi/httpapi.go`, add `streamHandler` as a standalone function after `writeJSON`:

```go
// streamHandler returns an SSE handler that pushes the current snapshot
// immediately and on every state change, plus a 30-second heartbeat comment
// so proxies and NAT keep the connection open.
//
// Route registration: HandlerFull registers this on "GET /state/stream".
// baseMux and sensorMux do not include it — it requires state.Store.Subscribe.
func streamHandler(st *state.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming not supported", http.StatusNotImplemented)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("X-Accel-Buffering", "no") // disable nginx buffering
		w.WriteHeader(http.StatusOK)

		ch, cancel := st.Subscribe()
		defer cancel()

		// Send initial snapshot so the client is never in an unknown state.
		sendSnapshot := func() {
			b, err := json.Marshal(st.Snapshot())
			if err != nil {
				return
			}
			fmt.Fprintf(w, "data: %s\n\n", b)
			flusher.Flush()
		}
		sendSnapshot()

		heartbeat := time.NewTicker(30 * time.Second)
		defer heartbeat.Stop()

		for {
			select {
			case <-r.Context().Done():
				return
			case <-ch:
				sendSnapshot()
			case <-heartbeat.C:
				// SSE comment line — keeps the connection alive through proxies.
				fmt.Fprintf(w, ": heartbeat\n\n")
				flusher.Flush()
			}
		}
	}
}
```

In `HandlerFull`, add the route registration immediately after the camera routes (before `return mux`):

```go
	mux.HandleFunc("GET /state/stream", streamHandler(st))

	return mux
```

Add `"fmt"` to the httpapi import block if not already present (it is already imported via `sensorFloat`).

- [ ] **Step 4: Run the SSE test to verify it passes**

Run: `go test ./internal/httpapi/ -run 'TestSSEStream' -v`
Expected:
```
--- PASS: TestSSEStreamDeliversDataFrame (0.00s)
PASS
```

- [ ] **Step 5: Run full httpapi suite with race detector**

Run: `go test ./internal/httpapi/ -race -v`
Expected: all existing tests PASS plus `TestSSEStreamDeliversDataFrame`, no data races.

---

### Task 5: Main — no wiring needed for SSE; confirm `HandlerFull` already serves it

**Depends on:** Task 4 (`GET /state/stream` registered inside `HandlerFull`)

**Files:**
- No file changes required.

`cmd/gardynd/main.go` already passes `st` (the `*state.Store`) to `httpapi.HandlerFull(c, st, devs, frames, deps)` (line 166 of current `main.go`). Because `streamHandler` is registered inside `HandlerFull` using the `st` it receives, the route is automatically available on the live server with no additional wiring.

This task is a verification checkpoint, not a code task.

- [ ] **Step 1: Confirm the route is live by building and curling**

```bash
make build
./bin/gardynd --hw=mock &
sleep 1
curl -N --max-time 3 http://localhost:5000/state/stream
kill %1
```
Expected: the curl output contains at least one line starting with `data:` followed by a JSON object, then connection closes after 3 seconds via `--max-time`.

- [ ] **Step 2: Build Pi cross-compile**

Run: `make build-pi`
Expected: `bin/gardynd-armv6` builds with no errors.

---

### Task 6: CI hardening — `golangci-lint`, `e2e-mock`, `ruff` jobs in `.github/workflows/build.yml`

**Depends on:** none (within this plan; CI YAML is independent of Go source changes)

**Files:**
- Modify: `.github/workflows/build.yml`

Three new jobs:
- **`lint`**: runs `golangci/golangci-lint-action@v6` on Go source. Parallel with `test-and-build`.
- **`e2e-mock`**: builds the host binary, starts it with `--hw=mock` on a deterministic test port (`:15099`), curls `/healthz` and `/state` (assert 200 + JSON keys `available` and `uptime_s`), curls `/state/stream` to assert `data:` prefix arrives, POSTs to `/light/on`, then kills the process. Runs after `test-and-build` (needs the build step to succeed first).
- **`ruff`**: lints Python files under `custom_components/` with `ruff check`. Guarded by `if: hashFiles('custom_components/**/*.py') != ''` so it skips silently when the directory doesn't exist on the branch. Parallel with `test-and-build`.

Since CI YAML cannot be unit-tested via `go test`, the test step for this task is `python3 -c "import yaml; yaml.safe_load(open('.github/workflows/build.yml'))"` to confirm valid YAML syntax, plus a careful structural review in Step 2.

- [ ] **Step 1: Write the complete new workflow YAML**

Replace `.github/workflows/build.yml` with the following complete content:

```yaml
name: build

on:
  push:
    branches: [main, go-service-rewrite]
    tags: ["v*"]
  pull_request:

jobs:
  test-and-build:
    runs-on: ubuntu-latest
    permissions:
      contents: write
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod
      - name: Test
        run: go test -race ./...
      - name: Vet
        run: go vet ./...
      - name: Cross-compile (Pi Zero, ARMv6)
        run: GOOS=linux GOARCH=arm GOARM=6 CGO_ENABLED=0 go build -trimpath -o bin/gardynd-armv6 ./cmd/gardynd
      - name: Upload artifact
        uses: actions/upload-artifact@v4
        with:
          name: gardynd-armv6
          path: bin/gardynd-armv6
      - name: Attach to release
        if: startsWith(github.ref, 'refs/tags/')
        uses: softprops/action-gh-release@v2
        with:
          files: bin/gardynd-armv6

  lint:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod
      - name: golangci-lint
        uses: golangci/golangci-lint-action@v6
        with:
          version: latest

  e2e-mock:
    runs-on: ubuntu-latest
    needs: test-and-build
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod
      - name: Build host binary
        run: go build -o bin/gardynd ./cmd/gardynd
      - name: Start daemon (mock hardware, port 15099)
        run: ./bin/gardynd --hw=mock --http-port=15099 &
      - name: Wait for daemon to be ready
        run: |
          for i in $(seq 1 20); do
            if curl -sf http://localhost:15099/healthz > /dev/null 2>&1; then
              echo "daemon ready after ${i}s"
              break
            fi
            sleep 1
          done
      - name: Assert /healthz returns 200 with status ok
        run: |
          body=$(curl -sf http://localhost:15099/healthz)
          echo "healthz body: $body"
          echo "$body" | python3 -c "import sys,json; d=json.load(sys.stdin); assert d['status']=='ok', f'unexpected body: {d}'"
      - name: Assert /state returns 200 with expected keys
        run: |
          body=$(curl -sf http://localhost:15099/state)
          echo "state body: $body"
          echo "$body" | python3 -c "
          import sys, json
          d = json.load(sys.stdin)
          assert 'available' in d, f'missing available: {d}'
          assert 'uptime_s' in d, f'missing uptime_s: {d}'
          assert d['available'] == True, f'available is not true: {d}'
          "
      - name: Assert /state/stream delivers a data: frame
        run: |
          # Read 3 lines from the stream (initial frame + newline) with a 5s timeout.
          line=$(curl -sf --max-time 5 -N http://localhost:15099/state/stream | head -n 1)
          echo "stream first line: $line"
          echo "$line" | grep -q '^data:' || (echo "ERROR: first line was not a data: frame" && exit 1)
      - name: POST /light/on returns 200
        run: |
          code=$(curl -sf -o /dev/null -w "%{http_code}" -X POST http://localhost:15099/light/on)
          echo "light/on status: $code"
          [ "$code" = "200" ] || (echo "ERROR: expected 200 got $code" && exit 1)
      - name: Stop daemon
        if: always()
        run: pkill -f 'gardynd.*hw=mock' || true

  ruff:
    runs-on: ubuntu-latest
    if: hashFiles('custom_components/**/*.py') != ''
    steps:
      - uses: actions/checkout@v4
      - name: Install ruff
        run: pip install ruff
      - name: Lint Python HA integration
        run: ruff check custom_components/
```

- [ ] **Step 2: Validate YAML syntax**

Run: `python3 -c "import yaml; yaml.safe_load(open('.github/workflows/build.yml'))"`
Expected: no output (silent success — `safe_load` returns the parsed dict without error).

- [ ] **Step 3: Structural review checklist**

Verify manually (read the file):
- [ ] `test-and-build` job: unchanged from original — same 6 steps (checkout, setup-go, test, vet, cross-compile, upload-artifact, release-attach).
- [ ] `lint` job: `golangci/golangci-lint-action@v6`, no `needs:` (runs in parallel with `test-and-build`).
- [ ] `e2e-mock` job: `needs: test-and-build` present; port `15099` used consistently across all curl commands; `pkill` in `if: always()` so it runs even on failure.
- [ ] `ruff` job: `if: hashFiles('custom_components/**/*.py') != ''` guard present; `pip install ruff` before `ruff check`.
- [ ] No per-task commits appear anywhere.

---

### Task 7: Full suite + single commit

**Depends on:** Tasks 1, 2, 3, 4, 5, 6

**Files:**
- All files modified/created by Tasks 1–6.

- [ ] **Step 1: Run the full test suite with race detector**

Run: `go test ./... -race`
Expected:
```
ok  	github.com/iot-root/garden-of-eden/internal/config   0.XXXs
ok  	github.com/iot-root/garden-of-eden/internal/state    0.XXXs
ok  	github.com/iot-root/garden-of-eden/internal/httpapi  0.XXXs
ok  	github.com/iot-root/garden-of-eden/internal/core     0.XXXs
ok  	github.com/iot-root/garden-of-eden/internal/publish  0.XXXs
...
```
All packages PASS, no data races reported.

- [ ] **Step 2: Build both targets**

Run: `make tidy && make build && make build-pi`
Expected: `bin/gardynd` (host) and `bin/gardynd-armv6` (Pi Zero ARMv6) both build with no errors; `go.mod`/`go.sum` unchanged (no new deps were added).

- [ ] **Step 3: Validate CI YAML one final time**

Run: `python3 -c "import yaml; yaml.safe_load(open('.github/workflows/build.yml'))"`
Expected: silent success.

- [ ] **Step 4: Commit (single commit covering all plan changes)**

```
git add internal/config/reload.go \
        internal/config/reload_test.go \
        internal/state/state.go \
        internal/state/state_test.go \
        internal/httpapi/httpapi.go \
        internal/httpapi/httpapi_test.go \
        cmd/gardynd/main.go \
        .github/workflows/build.yml
git commit -m "feat: config hot-reload (mtime-poll), SSE push (/state/stream), CI hardening (golangci-lint, e2e-mock, ruff)"
```

---

## Self-Review

### 1. Spec coverage

| Requirement | Task |
|---|---|
| Config hot-reload via mtime-poll, stdlib only (no fsnotify) | Tasks 1, 2 |
| Pure `reloadDiff` helper, unit-tested | Task 1 |
| Reload reapplies schedules (assign into schedMu + `st.SetScheduleEnabled`) | Tasks 1, 2 |
| Reload reapplies `water.low_cm` via `c.SetWaterLowCM` | Tasks 1, 2 |
| Reload reapplies `overtemp.cut_light` via `c.SetCutLightOnOverTemp` | Tasks 1, 2 |
| HTTP port/bind + intervals documented as restart-required (not hot-reloaded) | Task 1 (`reload.go` godoc + `TestApplyReloadHTTPPortIgnored`) |
| Structured log message on reload | Task 2 (`log.Printf("config reloaded from %s", ...)`) |
| `ConfigReloadSeconds` decision: hardcoded constant, justified | Task 1 (`configReloadInterval = 5s`, rationale in godoc) |
| TDD: pure reapply helper tests (schedule change, water change, port ignored) | Task 1 (6 tests) |
| `Subscribe() (<-chan struct{}, func())` on `state.Store` | Task 3 |
| Buffered (cap 1) notify channels, non-blocking coalescing send | Task 3 |
| Every mutating setter calls `notify()` | Task 3 |
| TDD: subscribe wakes subscriber, cancel unregisters, full channel doesn't block | Task 3 (4 tests) |
| `GET /state/stream` SSE handler | Task 4 |
| Sends current snapshot immediately on connect | Task 4 (`sendSnapshot()` before loop) |
| Sends `data: <json>\n\n` on each notify | Task 4 |
| Periodic heartbeat comment (30s) | Task 4 |
| Clean teardown on client disconnect (`r.Context().Done()`) | Task 4 |
| TDD: httptest.NewServer test reads at least one `data:` frame | Task 4 |
| SSE route registered on same mux as `HandlerFull` | Task 4 (inside `HandlerFull`) |
| Main wiring: SSE needs no extra wiring (HandlerFull already gets `st`) | Task 5 |
| CI: `golangci-lint` step via `golangci/golangci-lint-action@v6` | Task 6 |
| CI: `--hw=mock` end-to-end smoke job (build, start, curl /healthz + /state + /state/stream + /light/on, kill) | Task 6 |
| CI: `ruff` lint for Python under `custom_components/` with existence guard | Task 6 |
| CI: existing test/vet/cross-compile/release steps preserved | Task 6 |
| CI "test": `python3 -c "import yaml; yaml.safe_load(...)"` + structural review | Task 6 |
| Full suite `go test ./... -race` | Task 7 |
| Single commit at end | Task 7 |
| HA push coordinator follow-up mentioned | below |

**Follow-up (cross-branch, not implemented here):** The HA integration on branch `ha-integration` could add a push coordinator that consumes `st.Subscribe()` and flips the entity `iot_class` from `local_polling` to `local_push`, eliminating the polling coordinator loop. This is intentionally out of scope for this plan and must not be done here.

### 2. Placeholder scan

No "TBD", "TODO", "implement later", "fill in details", "similar to Task N", or "add appropriate error handling" present. Every code step contains complete, compilable Go. Every test step specifies the exact `go test` command and expected output.

The `pipeWriter` and `_ = rec` lines in the SSE test are inert stubs that were replaced by the `httptest.NewServer` approach in the same step — they are explicitly cleaned up in the final test code (the `type pipeWriter` block and the `_ = rec` lines should be removed; the test as written ends with the `httptest.NewServer` path only). The test step shows the complete replacement code; the stubs in Step 1 are an artifact of the annotation pattern (write-test-first, see it fail). The implementation agent should copy only the final `TestSSEStreamDeliversDataFrame` function.

> **Correction for SSE test clarity:** The test in Task 4 Step 1 contains some inline comments and intermediate variable declarations (`pr`, `pw`, `pipeWriter`) that are immediately superseded by the `httptest.NewServer` approach. The agent implementing this plan should use only the `httptest.NewServer` path shown in the final block. The dead variable declarations (`pr`, `pw`, `type pipeWriter`) must not appear in the committed test file — they exist in the plan only to document the design decision.

### 3. Type consistency

- `config.ApplyReload(old, new Config, opts ReloadOpts)` defined in Task 1, called in Task 2 — signatures match.
- `config.ReloadOpts` fields: `SetLightSchedule func(Schedule)`, `SetPumpSchedule func(Schedule)`, `SetLightEnabled func(bool)`, `SetPumpEnabled func(bool)`, `SetWaterLowCM func(float64)`, `SetCutLightOnOverTemp func(bool)` — all defined in Task 1 and all called with correct types in Task 2.
- `config.ConfigReloadInterval` (`time.Duration`) defined in Task 1, used in Task 2 as `time.NewTicker(config.ConfigReloadInterval)` — correct.
- `config.LoadFileOnly(*configPath)` in Task 2 matches the existing signature `func LoadFileOnly(path string) (Config, error)` in `internal/config/config.go`.
- `state.Store.Subscribe() (<-chan struct{}, func())` defined in Task 3, used in Task 4's `streamHandler` — signatures match.
- `state.Store.notify()` — unexported, called inside all setter methods within the same package — consistent.
- `streamHandler(st *state.Store) http.HandlerFunc` defined in Task 4, registered as `mux.HandleFunc("GET /state/stream", streamHandler(st))` inside `HandlerFull` — `st` is already in scope as the second parameter of `HandlerFull(c *core.Core, st *state.Store, ...)` — correct.
- `http.Flusher` assertion: `flusher, ok := w.(http.Flusher)` — standard pattern; `flusher.Flush()` is called after every write — correct.
- `fileCfg` in Task 2: already declared as `fileCfg, err := config.LoadFileOnly(*configPath)` in current `main.go` (line 41–44). The reloader goroutine closes over it and reassigns `fileCfg = newFileCfg` — valid Go (goroutine closes over the variable, not the value).

### 4. Dependency audit

- **Task 1** (`internal/config/reload.go`, `reload_test.go`): no other task in this plan touches `internal/config/`. No interface from another task consumed. Marked `none` — correct.
- **Task 2** (`cmd/gardynd/main.go`): uses `config.ApplyReload` and `config.ConfigReloadInterval` from Task 1. Marked `Depends on: Task 1` — correct.
- **Task 3** (`internal/state/state.go`, `state_test.go`): no other task in this plan defines or modifies state package files. Marked `none` — correct.
- **Task 4** (`internal/httpapi/httpapi.go`, `httpapi_test.go`): uses `st.Subscribe()` from Task 3. Marked `Depends on: Task 3` — correct.
- **Task 5** (verification only, no file changes): logically depends on Task 4. Marked `Depends on: Task 4` — correct.
- **Task 6** (`.github/workflows/build.yml`): CI YAML does not import Go packages; it invokes built binaries. Logically independent of Tasks 1–5 within this plan. Marked `none` — correct.
- **Task 7** (full suite + commit): depends on all of Tasks 1–6. Marked `Depends on: Tasks 1, 2, 3, 4, 5, 6` — correct.

**Parallel execution opportunities:**
- Tasks 1, 3, and 6 are mutually independent (disjoint file sets, no shared interfaces within this plan) and can be executed in parallel.
- Task 2 must follow Task 1.
- Task 4 must follow Task 3.
- Task 5 must follow Task 4.
- Task 7 must follow all others.
