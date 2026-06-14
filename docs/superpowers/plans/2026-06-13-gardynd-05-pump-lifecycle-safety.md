# gardynd Plan 5 — Pump & Lifecycle Safety Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make pump operation safe across the whole process lifecycle: a configurable pump max-runtime, a fail-closed water-sensor interlock that records `water.sensor_ok`, a clean-shutdown pump-off, a crash-surviving restart-enforced runtime failsafe persisted to disk, and a graceful HTTP/goroutine shutdown so no path can leave the pump running.

**Architecture:** All pump mutation continues to flow through the single-writer `core` goroutine; the interlock and failsafe remain core-goroutine work, while the failsafe timer callback only does a channel-safe `Submit`. Runtime-tunable knobs stay behind `cfgMu`. A tiny best-effort persistence module (`internal/core/pumpstate.go`) records the pump-on start time with an atomic temp+rename write so a crashed process can re-enforce max-runtime on restart. `main.go` owns ordered shutdown: drain HTTP, drive the pump off, then stop the background goroutines.

**Tech Stack:** Go stdlib only (`time`, `os`, `encoding/json`, `net/http`, `context`, `path/filepath`); no new dependencies. Same patterns as Plans 1–3: `cfgMu`-guarded core config, `envInt` in config, atomic temp+rename like `config.Save`.

**Spec:** `docs/superpowers/specs/2026-06-12-gardynd-go-service-design.md`
**Depends on:** Plan 1 (core, state store, httpapi, config), Plan 2 (sensor interfaces + mocks on `hw.Devices`), Plan 3 (scheduler, publisher, `HandlerFull`, `ControlDeps`, water-low interlock, in-process failsafe).

---

## File Structure (this plan)

```
internal/config/config.go         MODIFY: add PumpConfig (max_runtime_seconds, state_file); add WaterConfig.BlockOnSensorError; defaults; applyEnv hooks
internal/config/config_test.go    MODIFY: append PumpConfig default/env + BlockOnSensorError default/env tests
internal/state/state.go           MODIFY: add WaterState.SensorOK + SetWaterSensorOK setter
internal/state/state_test.go      MODIFY: append SetWaterSensorOK test
internal/hw/mock/mock.go          MODIFY: add Distance.Err field returned by MeasureCM
internal/core/core.go             MODIFY: add blockOnSensorError + SetBlockOnSensorError; rework applyPump ActionOn sensor-error fail-policy + SensorOK recording; persist pump-on start / clear on off; arm failsafe for remaining time
internal/core/pumpstate.go        CREATE: pure runtime-state file (write/read/clear) + pure ShouldForceOff decision fn
internal/core/pumpstate_test.go   CREATE: write/read/clear round-trip + ShouldForceOff table tests
internal/core/core_test.go        MODIFY: append interlock fail-policy tests (blocked-on-error vs fail-open) + SensorOK + persistence-on-on/off tests
cmd/gardynd/main.go               MODIFY: configurable max-runtime; SetBlockOnSensorError; startup restart-check; clean-shutdown pump-off; graceful HTTP shutdown; stoppable button goroutine; shutdown ordering
```

---

### Task 1: Config — `PumpConfig` + `WaterConfig.BlockOnSensorError`

**Depends on:** none (within this plan; touches only `internal/config/`)

Add a `PumpConfig` (`max_runtime_seconds` default 600, `state_file` default `/run/gardynd/pump.json`; env `PUMP_MAX_RUNTIME_SECONDS` and `PUMP_STATE_FILE`) and a `Pump PumpConfig` field on `Config`. Add `BlockOnSensorError bool` to `WaterConfig` (default **true** — fail-closed: a distance-sensor read error blocks the pump; for a dry-run guard refusing to run is safer than running blind). Env `WATER_BLOCK_ON_SENSOR_ERROR` via a new `envBool` helper.

> Tradeoff (document in the field comment): fail-closed means a flaky sensor can prevent watering until it recovers. We accept that — an over-watered/dry-pumped reservoir is the worse failure. Operators who prefer availability over the dry-run guard can set `block_on_sensor_error: false`.

- [ ] **Step 1: Write the failing tests (append to config_test.go)**

Append to `internal/config/config_test.go`:
```go
func TestPumpMaxRuntimeDefault(t *testing.T) {
	c, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if c.Pump.MaxRuntimeSeconds != 600 {
		t.Errorf("Pump.MaxRuntimeSeconds default = %d, want 600", c.Pump.MaxRuntimeSeconds)
	}
}

func TestPumpStateFileDefault(t *testing.T) {
	c, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if c.Pump.StateFile != "/run/gardynd/pump.json" {
		t.Errorf("Pump.StateFile default = %q, want /run/gardynd/pump.json", c.Pump.StateFile)
	}
}

func TestPumpMaxRuntimeEnvOverride(t *testing.T) {
	t.Setenv("PUMP_MAX_RUNTIME_SECONDS", "120")
	t.Setenv("PUMP_STATE_FILE", "/tmp/pump.json")
	c, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if c.Pump.MaxRuntimeSeconds != 120 {
		t.Errorf("Pump.MaxRuntimeSeconds = %d, want 120", c.Pump.MaxRuntimeSeconds)
	}
	if c.Pump.StateFile != "/tmp/pump.json" {
		t.Errorf("Pump.StateFile = %q, want /tmp/pump.json", c.Pump.StateFile)
	}
}

func TestBlockOnSensorErrorDefaultTrue(t *testing.T) {
	c, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if !c.Water.BlockOnSensorError {
		t.Errorf("Water.BlockOnSensorError default = false, want true (fail-closed)")
	}
}

func TestBlockOnSensorErrorEnvOverride(t *testing.T) {
	t.Setenv("WATER_BLOCK_ON_SENSOR_ERROR", "false")
	c, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if c.Water.BlockOnSensorError {
		t.Errorf("Water.BlockOnSensorError = true after env=false, want false")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config/ -run 'TestPump|TestBlockOnSensorError' -v`
Expected: build failure — `c.Pump undefined` and `c.Water.BlockOnSensorError undefined`.

- [ ] **Step 3: Add the types, fields, defaults, env hooks, and `envBool`**

In `internal/config/config.go`, extend `WaterConfig`:
```go
type WaterConfig struct {
	LowCM float64 `yaml:"low_cm"` // 0 disables the interlock
	// BlockOnSensorError, when true (default), refuses to start the pump if the
	// distance sensor errors while the interlock is enabled (fail-closed: a
	// dry-run guard that cannot read the level should not run the pump). Set
	// false to fail-open and pump anyway when the sensor is unreadable.
	BlockOnSensorError bool `yaml:"block_on_sensor_error"`
}
```

Add the `PumpConfig` type (place it after `WaterConfig`):
```go
type PumpConfig struct {
	// MaxRuntimeSeconds bounds a single continuous pump run; 0 disables the
	// failsafe. Default 600 (10 minutes).
	MaxRuntimeSeconds int `yaml:"max_runtime_seconds"`
	// StateFile persists the pump-on start time so max-runtime can be enforced
	// across a crash/restart (the in-process timer dies with the process).
	StateFile string `yaml:"state_file"`
}
```

Add the `Pump` field to `Config`:
```go
type Config struct {
	HTTP       HTTPConfig     `yaml:"http"`
	Device     DeviceConfig   `yaml:"device"`
	Camera     CameraConfig   `yaml:"camera"`
	SensorType string         `yaml:"sensor_type"`
	Schedules  Schedules      `yaml:"schedules"`
	Water      WaterConfig    `yaml:"water"`
	Pump       PumpConfig     `yaml:"pump"`
	OverTemp   OverTempConfig `yaml:"overtemp"`
}
```

In `defaults()`, set the new defaults (note `Water.BlockOnSensorError: true`):
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
		Water:      WaterConfig{BlockOnSensorError: true},
		Pump:       PumpConfig{MaxRuntimeSeconds: 600, StateFile: "/run/gardynd/pump.json"},
	}
}
```

In `applyEnv`, add the three hooks (after the existing `envFloat(&c.Water.LowCM, ...)`):
```go
	envFloat(&c.Water.LowCM, "WATER_LOW_CM")
	envBool(&c.Water.BlockOnSensorError, "WATER_BLOCK_ON_SENSOR_ERROR")
	envInt(&c.Pump.MaxRuntimeSeconds, "PUMP_MAX_RUNTIME_SECONDS")
	envStr(&c.Pump.StateFile, "PUMP_STATE_FILE")
```

Add the `envBool` helper next to `envFloat`:
```go
func envBool(dst *bool, key string) {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			*dst = b
		}
	}
}
```

> Note: `BlockOnSensorError` defaults to `true` only via `defaults()`. A YAML file that omits the key inherits the default (the file is unmarshalled *over* `defaults()` in `LoadFileOnly`), so an explicit `block_on_sensor_error: false` is required to opt out. This matches how every other field in this config behaves.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/config/ -run 'TestPump|TestBlockOnSensorError' -v`
Expected:
```
--- PASS: TestPumpMaxRuntimeDefault (0.00s)
--- PASS: TestPumpStateFileDefault (0.00s)
--- PASS: TestPumpMaxRuntimeEnvOverride (0.00s)
--- PASS: TestBlockOnSensorErrorDefaultTrue (0.00s)
--- PASS: TestBlockOnSensorErrorEnvOverride (0.00s)
PASS
```

- [ ] **Step 5: Run full config suite to confirm no regressions**

Run: `go test ./internal/config/ -v`
Expected: all existing tests PASS plus the five new ones.

---

### Task 2: State store — `WaterState.SensorOK` + `SetWaterSensorOK`

**Depends on:** none (within this plan; touches only `internal/state/`)

Add `SensorOK bool` (json `sensor_ok`) to `WaterState`, and a `Store.SetWaterSensorOK(bool)` setter that updates only that field (so the core can record sensor health without disturbing the threshold/low values written by `SetWater`).

- [ ] **Step 1: Write the failing test (append to state_test.go)**

Append to `internal/state/state_test.go`:
```go
func TestSetWaterSensorOK(t *testing.T) {
	s := New()
	s.SetWater(10.0, true) // threshold=10, low=true
	s.SetWaterSensorOK(false)
	snap := s.Snapshot()
	if snap.Water.SensorOK {
		t.Errorf("SensorOK = true, want false")
	}
	// SetWaterSensorOK must not clobber the threshold/low set by SetWater.
	if snap.Water.LowThresholdCM != 10.0 || !snap.Water.Low {
		t.Errorf("SetWaterSensorOK clobbered other fields: %+v", snap.Water)
	}
	s.SetWaterSensorOK(true)
	if !s.Snapshot().Water.SensorOK {
		t.Errorf("SensorOK = false after set true")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/state/ -run TestSetWaterSensorOK -v`
Expected: build failure — `s.SetWaterSensorOK undefined` (and `snap.Water.SensorOK undefined`).

- [ ] **Step 3: Add the field and setter to state.go**

In `internal/state/state.go`, extend `WaterState`:
```go
type WaterState struct {
	LowThresholdCM float64 `json:"low_threshold_cm"`
	Low            bool    `json:"low"`
	SensorOK       bool    `json:"sensor_ok"`
}
```

Add the setter below the existing `SetWater`:
```go
// SetWaterSensorOK records whether the last distance-sensor read succeeded,
// without disturbing the threshold/low fields set by SetWater.
func (s *Store) SetWaterSensorOK(ok bool) {
	s.mu.Lock()
	s.snap.Water.SensorOK = ok
	s.mu.Unlock()
}
```

> Note: `SetWater` constructs a fresh `WaterState{LowThresholdCM, Low}`, which zeroes `SensorOK`. The core (Task 4) always calls `SetWaterSensorOK` *after* any `SetWater` in the interlock path, so the final stored value is correct. This ordering is asserted in Task 4's tests.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/state/ -run TestSetWaterSensorOK -v`
Expected:
```
--- PASS: TestSetWaterSensorOK (0.00s)
PASS
```

- [ ] **Step 5: Run full state suite to confirm no regressions**

Run: `go test ./internal/state/ -v`
Expected: all existing tests PASS plus the new one.

---

### Task 3: Mock — make `Distance` able to return an error

**Depends on:** none (within this plan; touches only `internal/hw/mock/`)

The core interlock tests in Task 4 need a distance sensor that *errors*. The current `mock.Distance.MeasureCM` always returns a nil error. Add an `Err error` field; when set, `MeasureCM` returns `(0, Err)`.

- [ ] **Step 1: Write the failing test (append to a new mock test file)**

Create `internal/hw/mock/mock_test.go`:
```go
package mock

import (
	"errors"
	"testing"
)

func TestDistanceReturnsErrWhenSet(t *testing.T) {
	sentinel := errors.New("sensor offline")
	d := &Distance{Err: sentinel}
	cm, err := d.MeasureCM()
	if err != sentinel {
		t.Errorf("err = %v, want %v", err, sentinel)
	}
	if cm != 0 {
		t.Errorf("cm = %v, want 0 on error", cm)
	}
}

func TestDistanceNoErrByDefault(t *testing.T) {
	d := &Distance{CM: 5.0}
	cm, err := d.MeasureCM()
	if err != nil {
		t.Errorf("err = %v, want nil", err)
	}
	if cm != 5.0 {
		t.Errorf("cm = %v, want 5.0", cm)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/hw/mock/ -run TestDistance -v`
Expected: build failure — `unknown field Err in struct literal of type Distance`.

- [ ] **Step 3: Add the `Err` field to mock.Distance**

In `internal/hw/mock/mock.go`, replace the `Distance` type and its `MeasureCM`:
```go
type Distance struct {
	CM  float64
	Err error
}

func (d *Distance) MeasureCM() (float64, error) {
	if d.Err != nil {
		return 0, d.Err
	}
	if d.CM == 0 {
		return 7.5, nil
	}
	return d.CM, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/hw/mock/ -run TestDistance -v`
Expected:
```
--- PASS: TestDistanceReturnsErrWhenSet (0.00s)
--- PASS: TestDistanceNoErrByDefault (0.00s)
PASS
```

---

### Task 4: Core — interlock fail-policy + `SensorOK` recording

**Depends on:** Task 1 (config not needed at compile time, but the policy mirrors `WaterConfig.BlockOnSensorError`), Task 2 (`SetWaterSensorOK`), Task 3 (mock `Distance.Err`)

Add a `cfgMu`-guarded `blockOnSensorError` knob and `SetBlockOnSensorError`. Rework `applyPump` `ActionOn`: when the interlock is enabled and `measureDistance` errors, either refuse (block-on-error: record `sensor_ok=false`, flash, return) or proceed (fail-open: record `sensor_ok=false`, continue). On a successful read, record `sensor_ok=true`.

- [ ] **Step 1: Write the failing tests (append to core_test.go)**

Append to `internal/core/core_test.go` (the file already imports `mockhw "github.com/iot-root/garden-of-eden/internal/hw/mock"`, `state`, `time`, and `testing` from Plan 3; do not re-add them):
```go
func TestPumpBlockedOnSensorErrorFailClosed(t *testing.T) {
	st := state.New()
	devs := mockhw.New()
	devs.Distance.(*mockhw.Distance).Err = errExampleSensor
	c := New(devs, st)
	c.SetWaterLowCM(10.0)
	c.SetBlockOnSensorError(true) // fail-closed
	go c.Run()
	defer c.Stop()

	c.Submit(Command{Target: TargetPump, Action: ActionOn})
	time.Sleep(2 * time.Second) // flashLights blocks ~1.8s

	snap := st.Snapshot()
	if snap.Pump.On {
		t.Error("pump turned on despite sensor error (fail-closed)")
	}
	if snap.Water.SensorOK {
		t.Error("water.sensor_ok should be false after a read error")
	}
	if devs.Pump.(*mockhw.Pump).Speed() != 0 {
		t.Error("pump hardware was driven despite sensor error")
	}
}

func TestPumpAllowedOnSensorErrorFailOpen(t *testing.T) {
	st := state.New()
	devs := mockhw.New()
	devs.Distance.(*mockhw.Distance).Err = errExampleSensor
	c := New(devs, st)
	c.SetWaterLowCM(10.0)
	c.SetBlockOnSensorError(false) // fail-open: pump anyway
	go c.Run()
	defer c.Stop()

	c.Submit(Command{Target: TargetPump, Action: ActionOn})
	for i := 0; i < 50; i++ {
		if st.Snapshot().Pump.On {
			if st.Snapshot().Water.SensorOK {
				t.Error("water.sensor_ok should be false even when failing open")
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Error("pump should have turned on (fail-open)")
}

func TestPumpSensorOKTrueOnGoodRead(t *testing.T) {
	st := state.New()
	devs := mockhw.New()
	devs.Distance.(*mockhw.Distance).CM = 5.0 // <= threshold => ok, no error
	c := New(devs, st)
	c.SetWaterLowCM(10.0)
	go c.Run()
	defer c.Stop()

	c.Submit(Command{Target: TargetPump, Action: ActionOn})
	for i := 0; i < 50; i++ {
		snap := st.Snapshot()
		if snap.Pump.On {
			if !snap.Water.SensorOK {
				t.Error("water.sensor_ok should be true after a good read")
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Error("pump should have turned on")
}

var errExampleSensor = errSensorTest{}

type errSensorTest struct{}

func (errSensorTest) Error() string { return "mock distance sensor error" }
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/core/ -run 'SensorError|SensorOK' -v`
Expected: build failure — `c.SetBlockOnSensorError undefined`.

- [ ] **Step 3: Add the knob and rework `applyPump` ActionOn**

In `internal/core/core.go`, add the field to the `cfgMu` group of `Core`:
```go
	cfgMu              sync.Mutex // guards runtime-tunable config
	waterLowCM         float64
	pumpMaxRuntime     time.Duration
	cutLightOnOverTemp bool
	blockOnSensorError bool
	pumpTimer          *time.Timer // core-goroutine only
```

Add the setter and getter (next to `SetCutLightOnOverTemp` / `cutLightOnTemp`):
```go
func (c *Core) SetBlockOnSensorError(b bool) {
	c.cfgMu.Lock()
	c.blockOnSensorError = b
	c.cfgMu.Unlock()
}

// blockOnError returns the sensor-error fail policy under cfgMu.
func (c *Core) blockOnError() bool {
	c.cfgMu.Lock()
	defer c.cfgMu.Unlock()
	return c.blockOnSensorError
}
```

Replace the `ActionOn` interlock block in `applyPump` (the `if threshold > 0 { ... }` section) with:
```go
	case ActionOn:
		threshold := c.waterLow()
		if threshold > 0 {
			cm, err := c.measureDistance()
			if err != nil {
				// Distance read failed. Record sensor health, then apply policy.
				c.store.SetWaterSensorOK(false)
				if c.blockOnError() {
					log.Printf("pump on: distance read failed, blocking (fail-closed): %v", err)
					c.flashLights()
					return
				}
				log.Printf("pump on: distance read failed, proceeding (fail-open): %v", err)
			} else {
				c.store.SetWaterSensorOK(true)
				if cm > threshold {
					c.store.SetWater(threshold, true) // low=true
					c.store.SetWaterSensorOK(true)     // SetWater zeroes SensorOK; restore it
					c.flashLights()
					return
				}
				c.store.SetWater(threshold, false)
				c.store.SetWaterSensorOK(true) // SetWater zeroes SensorOK; restore it
			}
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

(The `ActionOff` and `ActionSetLevel` cases are unchanged in this step.)

> Concurrency: every store call above runs on the core goroutine; `measureDistance` and `flashLights` are core-goroutine-only helpers (unchanged from Plan 3). `blockOnError`/`waterLow` read the knobs under `cfgMu`. The failsafe timer (unchanged) only does `Submit`, which is channel-safe.

- [ ] **Step 4: Run test to verify it passes (with race detector)**

Run: `go test ./internal/core/ -run 'SensorError|SensorOK' -race -v`
Expected:
```
--- PASS: TestPumpBlockedOnSensorErrorFailClosed (2.0Xs)
--- PASS: TestPumpAllowedOnSensorErrorFailOpen (0.0Xs)
--- PASS: TestPumpSensorOKTrueOnGoodRead (0.0Xs)
PASS
```

- [ ] **Step 5: Run the full core suite with race detector**

Run: `go test ./internal/core/ -race -v`
Expected: all Plan 3 interlock/failsafe tests PASS plus the three new ones; no races.

---

### Task 5: Pump-state persistence module (pure decision fn + atomic file IO)

**Depends on:** none (within this plan; new file in `internal/core/`, no shared symbols with Task 4 at this step)

Create `internal/core/pumpstate.go` with a tiny persisted record `{started_at}`, atomic write (temp+rename, mirroring `config.Save`), read, clear, and a **pure** `ShouldForceOff(startedAt, now, maxRuntime)` decision used by the restart check. Keep it best-effort: callers log on error and never block pump operation.

- [ ] **Step 1: Write the failing tests**

Create `internal/core/pumpstate_test.go`:
```go
package core

import (
	"path/filepath"
	"testing"
	"time"
)

func TestPumpStateWriteReadClear(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "pump.json") // sub dir must be created
	started := time.Now().Truncate(time.Second)

	if err := writePumpState(path, started); err != nil {
		t.Fatalf("writePumpState: %v", err)
	}
	got, ok, err := readPumpState(path)
	if err != nil {
		t.Fatalf("readPumpState: %v", err)
	}
	if !ok {
		t.Fatal("readPumpState ok=false, want true after write")
	}
	if !got.Equal(started) {
		t.Errorf("startedAt = %v, want %v", got, started)
	}

	if err := clearPumpState(path); err != nil {
		t.Fatalf("clearPumpState: %v", err)
	}
	if _, ok, _ := readPumpState(path); ok {
		t.Error("readPumpState ok=true after clear, want false")
	}
}

func TestReadPumpStateMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent.json")
	_, ok, err := readPumpState(path)
	if err != nil {
		t.Fatalf("readPumpState missing: err = %v, want nil", err)
	}
	if ok {
		t.Error("ok = true for missing file, want false")
	}
}

func TestClearPumpStateMissingIsNoError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent.json")
	if err := clearPumpState(path); err != nil {
		t.Errorf("clearPumpState missing: err = %v, want nil", err)
	}
}

func TestShouldForceOff(t *testing.T) {
	start := time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC)
	max := 10 * time.Minute
	cases := []struct {
		name string
		now  time.Time
		max  time.Duration
		want bool
	}{
		{"elapsed below max", start.Add(5 * time.Minute), max, false},
		{"elapsed equals max", start.Add(10 * time.Minute), max, true},
		{"elapsed above max", start.Add(11 * time.Minute), max, true},
		{"max disabled (0)", start.Add(99 * time.Hour), 0, false},
		{"clock skew: now before start", start.Add(-time.Minute), max, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ShouldForceOff(start, tc.now, tc.max); got != tc.want {
				t.Errorf("ShouldForceOff(%v) = %v, want %v", tc.now, got, tc.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/core/ -run 'PumpState|ShouldForceOff' -v`
Expected: build failure — `undefined: writePumpState`.

- [ ] **Step 3: Implement the persistence module**

Create `internal/core/pumpstate.go`:
```go
package core

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

// pumpRecord is the on-disk runtime state persisted while the pump is running.
// It lets a crashed/restarted process re-enforce the max-runtime failsafe (the
// in-process time.AfterFunc dies with the process).
type pumpRecord struct {
	StartedAt time.Time `json:"started_at"`
}

// writePumpState atomically records the pump-on start time at path (temp file in
// the same dir + rename, mirroring config.Save). It creates parent dirs as
// needed. Best-effort: callers log on error and never block pump operation.
func writePumpState(path string, startedAt time.Time) error {
	if path == "" {
		return nil
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(pumpRecord{StartedAt: startedAt})
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".pump-*.json")
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
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}

// readPumpState reads the persisted start time. ok=false (nil error) when the
// file does not exist. A malformed/unparseable file returns an error so the
// caller can log it and clear the file.
func readPumpState(path string) (startedAt time.Time, ok bool, err error) {
	if path == "" {
		return time.Time{}, false, nil
	}
	data, rerr := os.ReadFile(path)
	if rerr != nil {
		if errors.Is(rerr, os.ErrNotExist) {
			return time.Time{}, false, nil
		}
		return time.Time{}, false, rerr
	}
	var rec pumpRecord
	if jerr := json.Unmarshal(data, &rec); jerr != nil {
		return time.Time{}, false, jerr
	}
	if rec.StartedAt.IsZero() {
		return time.Time{}, false, nil
	}
	return rec.StartedAt, true, nil
}

// clearPumpState removes the runtime-state file. A missing file is not an error.
func clearPumpState(path string) error {
	if path == "" {
		return nil
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// ShouldForceOff reports whether a pump that started at startedAt has, by now,
// run at least maxRuntime and must be forced off. maxRuntime <= 0 disables the
// failsafe (returns false). Pure and unit-tested; safe against clock skew where
// now precedes startedAt (returns false).
func ShouldForceOff(startedAt, now time.Time, maxRuntime time.Duration) bool {
	if maxRuntime <= 0 {
		return false
	}
	elapsed := now.Sub(startedAt)
	if elapsed < 0 {
		return false
	}
	return elapsed >= maxRuntime
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/core/ -run 'PumpState|ShouldForceOff' -v`
Expected:
```
--- PASS: TestPumpStateWriteReadClear (0.00s)
--- PASS: TestReadPumpStateMissingFile (0.00s)
--- PASS: TestClearPumpStateMissingIsNoError (0.00s)
--- PASS: TestShouldForceOff (0.00s)
    --- PASS: TestShouldForceOff/elapsed_below_max (0.00s)
    --- PASS: TestShouldForceOff/elapsed_equals_max (0.00s)
    --- PASS: TestShouldForceOff/elapsed_above_max (0.00s)
    --- PASS: TestShouldForceOff/max_disabled_(0) (0.00s)
    --- PASS: TestShouldForceOff/clock_skew:_now_before_start (0.00s)
PASS
```

---

### Task 6: Core — persist on pump on/off, arm-for-remaining, restart enforcement

**Depends on:** Task 4 (reworked `applyPump`), Task 5 (`writePumpState`/`clearPumpState`/`readPumpState`/`ShouldForceOff`)

Wire persistence into the core: when the pump turns on, persist `started_at` and arm the failsafe; when it turns off, disarm and clear the file. Add a configurable `pumpStateFile` (set once at startup, read on the core goroutine only) and an exported restart helper `EnforcePumpRuntime` that main calls at boot.

- [ ] **Step 1: Write the failing tests (append to core_test.go)**

Append to `internal/core/core_test.go`:
```go
import "path/filepath" // add to the existing import block if not already present

func TestPumpOnPersistsStateOffClearsIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pump.json")
	st := state.New()
	c := New(mockhw.New(), st)
	c.SetPumpStateFile(path)
	c.SetPumpMaxRuntime(time.Hour) // long, so the failsafe does not fire mid-test
	go c.Run()
	defer c.Stop()

	c.Submit(Command{Target: TargetPump, Action: ActionOn})
	for i := 0; i < 50; i++ {
		if _, ok, _ := readPumpState(path); ok {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, ok, _ := readPumpState(path); !ok {
		t.Fatal("pump-on did not persist state file")
	}

	c.Submit(Command{Target: TargetPump, Action: ActionOff})
	for i := 0; i < 50; i++ {
		if _, ok, _ := readPumpState(path); !ok {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("pump-off did not clear state file")
}

func TestEnforcePumpRuntimeExpiredForcesOffAndClears(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pump.json")
	// Simulate a crash 20 minutes ago with a 10-minute max.
	if err := writePumpState(path, time.Now().Add(-20*time.Minute)); err != nil {
		t.Fatal(err)
	}
	st := state.New()
	devs := mockhw.New()
	devs.Pump.(*mockhw.Pump).SetSpeed(100) // pretend hardware is still running
	c := New(devs, st)

	remaining := c.EnforcePumpRuntime(path, 10*time.Minute, time.Now())

	if remaining != 0 {
		t.Errorf("remaining = %v, want 0 (expired)", remaining)
	}
	if devs.Pump.(*mockhw.Pump).Speed() != 0 {
		t.Error("expired pump was not driven off")
	}
	if _, ok, _ := readPumpState(path); ok {
		t.Error("state file not cleared after expiry")
	}
}

func TestEnforcePumpRuntimeRemainingReturnsRemainder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pump.json")
	// Started 3 minutes ago with a 10-minute max => ~7 min remaining.
	if err := writePumpState(path, time.Now().Add(-3*time.Minute)); err != nil {
		t.Fatal(err)
	}
	c := New(mockhw.New(), state.New())
	remaining := c.EnforcePumpRuntime(path, 10*time.Minute, time.Now())
	if remaining <= 6*time.Minute || remaining > 7*time.Minute {
		t.Errorf("remaining = %v, want ~7m", remaining)
	}
	// File must remain so a later disarm/clear path can remove it.
	if _, ok, _ := readPumpState(path); !ok {
		t.Error("state file cleared while still within max runtime")
	}
}

func TestEnforcePumpRuntimeNoFileReturnsZero(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent.json")
	c := New(mockhw.New(), state.New())
	if got := c.EnforcePumpRuntime(path, 10*time.Minute, time.Now()); got != 0 {
		t.Errorf("remaining = %v, want 0 when no file", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/core/ -run 'PersistsState|EnforcePumpRuntime' -v`
Expected: build failure — `c.SetPumpStateFile undefined` / `c.EnforcePumpRuntime undefined`.

- [ ] **Step 3: Add the state-file field, setter, persistence hooks, and `EnforcePumpRuntime`**

In `internal/core/core.go`, add a field to `Core` (NOT under `cfgMu` — set once at startup, then read only on the core goroutine in `applyPump`):
```go
	pumpTimer     *time.Timer // core-goroutine only
	pumpStateFile string      // core-goroutine only after startup
```

Add the setter (call it once at startup before submitting any pump command):
```go
// SetPumpStateFile sets the path used to persist the pump-on start time for
// restart-enforced failsafe. Call once at startup before the core handles any
// pump command; thereafter it is read only on the core goroutine.
func (c *Core) SetPumpStateFile(path string) { c.pumpStateFile = path }
```

In `applyPump`, persist on the successful `ActionOn` (immediately before `armPumpFailsafe`), and clear on `ActionOff`:
```go
		if err := c.dev.Pump.SetSpeed(c.pumpLevel); err != nil {
			log.Printf("pump on: %v", err)
			return
		}
		if c.pumpStateFile != "" {
			if err := writePumpState(c.pumpStateFile, time.Now()); err != nil {
				log.Printf("pump state persist (continuing): %v", err)
			}
		}
		c.armPumpFailsafe()
		c.store.SetPump(true, c.pumpLevel)
	case ActionOff:
		c.disarmPumpFailsafe()
		if c.pumpStateFile != "" {
			if err := clearPumpState(c.pumpStateFile); err != nil {
				log.Printf("pump state clear (continuing): %v", err)
			}
		}
		if err := c.dev.Pump.Off(); err != nil {
			log.Printf("pump off: %v", err)
			return
		}
		c.store.SetPump(false, c.pumpLevel)
```

> Best-effort persistence: a failed write/clear is logged and ignored; it never blocks driving the pump. The worst case of a failed write is that a crash would not re-enforce runtime (no worse than before this plan); the worst case of a failed clear is a stale file that the next startup's `EnforcePumpRuntime` will evaluate and, if expired, clear.

Add the exported restart helper. It runs on the calling goroutine at startup (before `Run`/any `Submit`), so it touches the device directly and does not use the command channel:
```go
// EnforcePumpRuntime is called once at startup to re-enforce the max-runtime
// failsafe across a crash/restart. If a persisted pump-on start time exists:
//   - if it has already run >= maxRuntime, the pump is driven OFF, the file is
//     cleared, and 0 is returned (caller should not arm a failsafe);
//   - otherwise the file is left in place and the REMAINING duration is
//     returned so the caller can arm a failsafe for that remainder.
// Returns 0 when there is no file (or it is unreadable). Best-effort: errors
// are logged, never fatal. Must run before c.Run handles any pump command.
func (c *Core) EnforcePumpRuntime(path string, maxRuntime time.Duration, now time.Time) time.Duration {
	startedAt, ok, err := readPumpState(path)
	if err != nil {
		log.Printf("pump state read at startup (clearing): %v", err)
		if cerr := clearPumpState(path); cerr != nil {
			log.Printf("pump state clear at startup: %v", cerr)
		}
		return 0
	}
	if !ok {
		return 0
	}
	if ShouldForceOff(startedAt, now, maxRuntime) {
		log.Printf("pump state: previous run exceeded max runtime (%s); forcing pump off", maxRuntime)
		if c.dev.Pump != nil {
			if oerr := c.dev.Pump.Off(); oerr != nil {
				log.Printf("pump off at startup: %v", oerr)
			}
		}
		c.store.SetPump(false, c.pumpLevel)
		if cerr := clearPumpState(path); cerr != nil {
			log.Printf("pump state clear at startup: %v", cerr)
		}
		return 0
	}
	return maxRuntime - now.Sub(startedAt)
}
```

> Why direct device access here is safe: `EnforcePumpRuntime` is documented to run at startup *before* `c.Run` is started (or before any pump command is submitted), so there is no concurrent core-goroutine writer to race with. This avoids a chicken-and-egg dependency on the command loop being live. The in-flight scheduler catch-up (Plan 3) runs after this and, if the schedule wants the pump on, will re-drive it through the normal `applyPump` path — which then persists a fresh start time and arms a full-length failsafe.

- [ ] **Step 4: Run test to verify it passes (with race detector)**

Run: `go test ./internal/core/ -run 'PersistsState|EnforcePumpRuntime' -race -v`
Expected:
```
--- PASS: TestPumpOnPersistsStateOffClearsIt (0.XXs)
--- PASS: TestEnforcePumpRuntimeExpiredForcesOffAndClears (0.00s)
--- PASS: TestEnforcePumpRuntimeRemainingReturnsRemainder (0.00s)
--- PASS: TestEnforcePumpRuntimeNoFileReturnsZero (0.00s)
PASS
```

- [ ] **Step 5: Run the full core suite with race detector**

Run: `go test ./internal/core/ -race`
Expected: all core tests PASS; no races.

---

### Task 7: main.go — configurable max-runtime, restart check, clean-shutdown pump-off, graceful HTTP shutdown, stoppable button

**Depends on:** Task 1 (`cfg.Pump`, `cfg.Water.BlockOnSensorError`), Task 4 (`SetBlockOnSensorError`), Task 6 (`SetPumpStateFile`, `EnforcePumpRuntime`)

Wire all the lifecycle safety into `cmd/gardynd/main.go`. This task has no unit test of its own (it is the composition root); correctness is verified by `go build` and the manual smoke test in Task 8.

- [ ] **Step 1: Configure the pump knobs and run the startup restart-check**

In `cmd/gardynd/main.go`, replace the three core-config lines:
```go
	// Core runtime config (setters are mutex-guarded, safe post-Run).
	c.SetWaterLowCM(cfg.Water.LowCM)
	c.SetPumpMaxRuntime(10 * time.Minute)
	c.SetCutLightOnOverTemp(cfg.OverTemp.CutLight)
```
with:
```go
	// Core runtime config (setters are mutex-guarded, safe post-Run).
	c.SetWaterLowCM(cfg.Water.LowCM)
	c.SetPumpMaxRuntime(time.Duration(cfg.Pump.MaxRuntimeSeconds) * time.Second)
	c.SetCutLightOnOverTemp(cfg.OverTemp.CutLight)
	c.SetBlockOnSensorError(cfg.Water.BlockOnSensorError)
	c.SetPumpStateFile(cfg.Pump.StateFile)
```

Then, AFTER `go c.Run()` has been started and the core config above is set, add the restart-enforcement check (the core loop is live, but no pump command has been submitted yet — the scheduler is started later). `EnforcePumpRuntime` runs on the main goroutine and touches the device directly only when forcing off an expired run; if a run is still within max-runtime, arm a failsafe for the remainder via the channel-safe path:
```go
	// Restart-enforced failsafe: if a previous run crashed while the pump was on,
	// enforce the remaining max-runtime (or force off if already expired).
	maxRT := time.Duration(cfg.Pump.MaxRuntimeSeconds) * time.Second
	if remaining := c.EnforcePumpRuntime(cfg.Pump.StateFile, maxRT, time.Now()); remaining > 0 {
		log.Printf("pump was running before restart; arming failsafe for remaining %s", remaining)
		time.AfterFunc(remaining, func() {
			c.Submit(core.Command{Target: core.TargetPump, Action: core.ActionOff})
		})
	}
```

> Placement: this block goes immediately after the `c.Set*` runtime-config calls and before the schedule wiring. `EnforcePumpRuntime` is safe to call here because no pump command has been submitted yet (the scheduler that might submit one is started further down). If the persisted run is still valid, we arm a one-shot timer for the remainder rather than re-running `armPumpFailsafe` (which would grant a full fresh interval).

- [ ] **Step 2: Make the button goroutine stoppable**

Replace the button block:
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
with a version that selects on a `done` channel so it exits on shutdown:
```go
	buttonDone := make(chan struct{})
	if devs.Button != nil {
		events := devs.Button.Events()
		go func() {
			for {
				select {
				case <-buttonDone:
					return
				case ev, ok := <-events:
					if !ok {
						return
					}
					switch ev {
					case hw.SinglePress:
						c.Submit(core.Command{Target: core.TargetLight, Action: core.ActionOn})
					case hw.DoublePress:
						c.Submit(core.Command{Target: core.TargetPump, Action: core.ActionOn})
					}
				}
			}
		}()
	}
```

- [ ] **Step 3: Capture the server and rewrite the shutdown sequence**

The `server := &http.Server{...}` and its `go func() { ListenAndServe }()` block already exist — keep them. Replace the final three lines:
```go
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	log.Printf("shutting down")
```
with the ordered shutdown:
```go
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	log.Printf("shutting down")

	// 1. Stop accepting new HTTP requests and drain in-flight ones, so no late
	//    request can turn the pump back on after we drive it off.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Printf("http shutdown: %v", err)
	}

	// 2. Stop the button goroutine so it cannot submit a pump-on after this.
	close(buttonDone)

	// 3. Stop the scheduler so a minute-boundary tick cannot re-drive the pump.
	sched.Stop()

	// 4. Drive the pump OFF and wait for the core to apply it. PWM hardware holds
	//    its last duty cycle across process exit, so a clean exit MUST turn the
	//    pump off explicitly. This also clears the persisted pump-state file via
	//    applyPump's ActionOff path.
	if !submitAndWaitPumpOff(c, st, 3*time.Second) {
		log.Printf("warning: pump did not confirm OFF within timeout")
	}

	// 5. Stop the publishers and core, then withdraw discovery.
	pub.Stop()
	c.Stop()
	stop()
}
```

Remove the existing `defer sched.Stop()`, `defer pub.Stop()`, `defer c.Stop()`, and `defer stop()` lines (their work now happens explicitly in the ordered sequence above so the ordering is guaranteed; deferred calls would otherwise run in reverse-declaration order and race the pump-off). Keep the `defer cleanup()` for `--hw=real` (it runs last, after `main` returns, releasing GPIO/PWM only after the pump is already off).

Add the synchronous pump-off helper near the top of the file (package-level function, after `main`):
```go
// submitAndWaitPumpOff submits a pump-OFF command and waits until the snapshot
// confirms the pump is off (or the timeout elapses). Reuses the single-writer
// command channel rather than touching the device directly, so the off goes
// through the same applyPump path that disarms the failsafe and clears the
// persisted pump-state file. Returns true if confirmed off.
func submitAndWaitPumpOff(c *core.Core, st *state.Store, timeout time.Duration) bool {
	c.Submit(core.Command{Target: core.TargetPump, Action: core.ActionOff})
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !st.Snapshot().Pump.On {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return !st.Snapshot().Pump.On
}
```

> Approach choice (Item #2a): we reuse the command channel (`submitAndWaitPumpOff`) rather than adding a direct-device `ShutdownPumpOff`. Justification: the core goroutine is still running at this point (we call `c.Stop()` only afterward), so the channel path is the safe single-writer route and it automatically disarms the failsafe and clears the persisted state file via the existing `ActionOff` case — a direct device write would duplicate that logic and risk racing the core goroutine. We bound the wait so a wedged core cannot hang shutdown.

- [ ] **Step 4: Fix imports**

Add `"context"` to the import block in `cmd/gardynd/main.go`. All other referenced packages (`core`, `state`, `hw`, `time`, `log`, `net/http`, `os`, `os/signal`, `syscall`) are already imported.

- [ ] **Step 5: Build to verify wiring compiles**

Run: `go build ./cmd/gardynd/`
Expected: builds with no errors.

---

### Task 8: Full suite (race) + single commit

**Depends on:** Tasks 1–7

- [ ] **Step 1: Build both targets**

Run: `make tidy && make build && make build-pi`
Expected: both the host `bin/gardynd` and the Pi binary build with no errors.

- [ ] **Step 2: Run the full test suite with the race detector**

Run: `go test ./... -race`
Expected: all packages PASS, no race conditions detected.

- [ ] **Step 3: Manual smoke test — clean-shutdown pump-off**

Run `./bin/gardynd --hw=mock --config /tmp/g.yaml` with `/tmp/g.yaml` containing:
```yaml
pump:
  max_runtime_seconds: 600
  state_file: /tmp/gardynd-pump.json
water:
  low_cm: 0   # disable interlock so the pump turns on in mock
```
Then:
```
curl -s -X POST http://localhost:5000/pump/on
cat /tmp/gardynd-pump.json   # should exist, containing {"started_at":...}
curl -s http://localhost:5000/state | python3 -m json.tool   # pump.on == true
```
Press Ctrl-C. Confirm the log shows the ordered shutdown ("shutting down", then pump confirmed off) and that `/tmp/gardynd-pump.json` is removed (clean-shutdown pump-off cleared it).

- [ ] **Step 4: Manual smoke test — restart-enforced failsafe (crash case)**

Simulate a crash leaving a stale state file older than max-runtime:
```
printf '{"started_at":"2000-01-01T00:00:00Z"}' > /tmp/gardynd-pump.json
./bin/gardynd --hw=mock --config /tmp/g.yaml &
sleep 1
curl -s http://localhost:5000/state | python3 -m json.tool   # pump.on == false (forced off)
test ! -f /tmp/gardynd-pump.json && echo "stale state cleared"
kill %1
```
Confirm the startup log shows "previous run exceeded max runtime; forcing pump off" and the file is cleared.

- [ ] **Step 5: Manual smoke test — sensor-error fail-closed**

With an interlock enabled and a sensor that errors, the pump must refuse. In mock you cannot inject a Distance error at runtime via REST, so this path is covered by the unit tests in Task 4 (`TestPumpBlockedOnSensorErrorFailClosed`). Confirm those pass under `-race` (Task 4 Step 4) — note this as the verification for Item #3's fail-closed behavior.

- [ ] **Step 6: Commit (single commit covering the whole plan)**

```
git add internal/config/config.go internal/config/config_test.go \
        internal/state/state.go internal/state/state_test.go \
        internal/hw/mock/mock.go internal/hw/mock/mock_test.go \
        internal/core/core.go internal/core/core_test.go \
        internal/core/pumpstate.go internal/core/pumpstate_test.go \
        cmd/gardynd/main.go
git commit -m "feat: pump lifecycle safety — configurable max-runtime, fail-closed sensor interlock, crash-surviving failsafe, graceful shutdown"
```

---

## Self-Review

**1. Spec coverage**

| Requirement (plan-05 scope item) | Task |
|---|---|
| #13 `PumpConfig.MaxRuntimeSeconds` yaml/env/default 600 | Task 1 |
| #13 `PumpConfig.StateFile` yaml/env/default `/run/gardynd/pump.json` | Task 1 |
| #13 main uses `SetPumpMaxRuntime(cfg.Pump.MaxRuntimeSeconds * time.Second)` | Task 7 Step 1 |
| #3 `WaterConfig.BlockOnSensorError` default true (fail-closed) + env | Task 1 |
| #3 `state.WaterState.SensorOK` + setter | Task 2 |
| #3 mock `Distance.Err` so tests can force a sensor failure | Task 3 |
| #3 core `SetBlockOnSensorError`; applyPump fail-closed/fail-open + SensorOK | Task 4 |
| #3 tests: blocked-on-error vs allowed-when-fail-open (-race) | Task 4 |
| #2b pure `ShouldForceOff` + atomic write/read/clear state file | Task 5 |
| #2b core persists start on pump-on, clears on pump-off | Task 6 |
| #2b `EnforcePumpRuntime` (expired→off+clear; valid→remaining) | Task 6 |
| #2b main startup restart check + arm-for-remaining | Task 7 Step 1 |
| #2a clean-shutdown pump-off via command channel, bounded wait | Task 7 Step 3 |
| #14 graceful HTTP `server.Shutdown(ctx)` with timeout | Task 7 Step 3 |
| #14 stoppable button goroutine (select on done) | Task 7 Step 2 |
| #14 main shutdown ordering (HTTP → button → sched → pump off → pub/core → discovery) | Task 7 Step 3 |
| Full suite `go test ./... -race` + single commit | Task 8 |

No gaps identified.

**2. Placeholder scan**

No "TBD"/"TODO"/"implement later". Every code step is complete compilable Go matching the real signatures read from source (`applyPump` cases, `cfgMu` field group, `store.SetWater`/`SetPump` shapes, `mock.Distance`, `config.defaults/applyEnv/Save` patterns, `main.go` shutdown block). Every test step has an exact `go test` command and expected output. The only forward references ("the scheduler is started later", "keep the existing `server :=` block") describe existing code in the file being modified, not undefined symbols.

**3. Type consistency**

- `PumpConfig{MaxRuntimeSeconds int, StateFile string}` and `WaterConfig.BlockOnSensorError bool` defined in Task 1; consumed in Task 7 as `time.Duration(cfg.Pump.MaxRuntimeSeconds)*time.Second`, `cfg.Pump.StateFile`, `cfg.Water.BlockOnSensorError` — types line up.
- `state.WaterState.SensorOK bool` + `Store.SetWaterSensorOK(bool)` (Task 2) used in core (Task 4) and asserted in Task 4 tests via `snap.Water.SensorOK`.
- `mock.Distance{CM float64, Err error}` (Task 3) used as `devs.Distance.(*mockhw.Distance).Err = ...` in Task 4 tests (matches the Plan 3 cast idiom `devs.Distance.(*mockhw.Distance).CM`).
- core additions: `SetBlockOnSensorError(bool)` (Task 4), `SetPumpStateFile(string)` + `EnforcePumpRuntime(string, time.Duration, time.Time) time.Duration` (Task 6) — all called with matching types in Task 7.
- `pumpstate.go` unexported `writePumpState(string, time.Time) error`, `readPumpState(string) (time.Time, bool, error)`, `clearPumpState(string) error`, exported `ShouldForceOff(time.Time, time.Time, time.Duration) bool` — used identically in pumpstate_test.go (Task 5), core.go (Task 6), core_test.go (Task 6).
- `submitAndWaitPumpOff(*core.Core, *state.Store, time.Duration) bool` (Task 7) uses `c.Submit`, `st.Snapshot().Pump.On` — both exist (core.go:65, state.go:39).
- `c.dev.Pump.Off()` and `c.store.SetPump(false, c.pumpLevel)` in `EnforcePumpRuntime` match `hw.Pump.Off()` and `Store.SetPump(bool,int)`.

All signatures match the source files read.

**4. Dependency audit**

- **Task 1** (`internal/config/`): no other task touches config files. `none` — correct.
- **Task 2** (`internal/state/`): no other task touches state files. `none` — correct.
- **Task 3** (`internal/hw/mock/`): no other task touches mock files. `none` — correct. (Task 4 *uses* the mock at test runtime but does not modify it; the dependency is real for execution order and is declared.)
- **Task 4** (`internal/core/core.go`, `core_test.go`): depends on Task 2 (`SetWaterSensorOK`) and Task 3 (`Distance.Err`). Declared.
- **Task 5** (`internal/core/pumpstate.go`, `pumpstate_test.go`): new files, no shared symbols with Task 4's edits at this step. `none` within plan — correct. (Tasks 4 and 5 both add files under `internal/core/` but to *different* files; `core_test.go` is appended by Task 4 and Task 6, so Tasks 4 and 6 are serialized on that file — see below.)
- **Task 6** (`internal/core/core.go`, `core_test.go`): depends on Task 4 (modifies the same `applyPump`/`Core` struct and appends to the same `core_test.go`) and Task 5 (`writePumpState`/`readPumpState`/`clearPumpState`/`ShouldForceOff`). Declared. Tasks 4 and 6 must be serialized (shared files); this is captured by Task 6's `Depends on: Task 4`.
- **Task 7** (`cmd/gardynd/main.go`): depends on Tasks 1, 4, 6. Declared.
- **Task 8**: depends on 1–7. Declared.
- Parallelism: Tasks 1, 2, 3, and 5 touch disjoint files and may run in parallel. Task 4 waits on 2+3; Task 6 waits on 4+5; Task 7 waits on 1+4+6; Task 8 last.

Audit clean.
