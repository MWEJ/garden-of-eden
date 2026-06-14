# gardynd Plan 6 — State Freshness & Device Contract Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the gardynd snapshot richer and more trustworthy for the HA integration: expose the device identity in `/state`, give telemetry its own configurable interval, standardize all error bodies on `{"error": ...}`, and recompute `water.low` on every publisher cycle rather than only on pump-on attempts.

**Architecture:** Four surgical, disjoint changes — config (new field), state store (new fields + setter), httpapi (error-key rename), and publish (recompute water.low in `publishOnce`) — wired together in a single final main.go update. No new packages. All changes are backward-compatible with the HA integration on branch `ha-integration` (that branch reads `/state` and errors; it benefits immediately once this lands).

**Tech Stack:** Go stdlib only; no new dependencies. Same patterns as Plans 1–3: mutex-guarded `state.Store` setters, `envInt`/`envStr` helpers in config, `writeJSON` in httpapi.

**Spec:** `docs/superpowers/specs/2026-06-12-gardynd-go-service-design.md`
**Depends on:** Plan 1 (core, state store, httpapi, config), Plan 2 (sensor interfaces, `sensorMux`), Plan 3 (publisher, scheduler, `HandlerFull`, `ControlDeps`).

**HA integration note:** The `ha-integration` branch reads `state["identifier"]` to populate `unique_id` in the HA config flow. Item B (device identity in `/state`) makes that value real. Item C (uniform `{"error": ...}` bodies) lets the HA side parse errors with a single key. Neither HA-side file is edited by this plan; those changes are on a separate branch.

---

## File Structure (this plan)

```
internal/config/config.go         MODIFY: add TelemetryIntervalSeconds field, applyEnv hook, Load clamp
internal/config/config_test.go    MODIFY: append telemetry interval tests
internal/state/state.go           MODIFY: add Identifier/Model/Version to Snapshot; add SetDeviceInfo setter
internal/state/state_test.go      MODIFY: append SetDeviceInfo test
internal/httpapi/httpapi.go       MODIFY: change "message" → "error" key in levelHandler error responses
internal/httpapi/httpapi_test.go  MODIFY: assert error bodies use "error" key
internal/publish/publish.go       MODIFY: recompute water.low after Distance read in publishOnce
internal/publish/publish_test.go  MODIFY: append two water.low recompute tests
cmd/gardynd/main.go               MODIFY: pass TelemetryIntervalSeconds to publish.New; call st.SetDeviceInfo
```

---

### Task 1: Config — `TelemetryIntervalSeconds` field

**Depends on:** none (within this plan; touches only `internal/config/`)

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`

Add `TelemetryIntervalSeconds` to `Config` (yaml `telemetry_interval_seconds`, env `TELEMETRY_INTERVAL_SECONDS`, default 30, clamped to ≥ 1 just like `Camera.IntervalSeconds`). `main.go` will use it to set the publisher interval (Task 5).

- [ ] **Step 1: Write the failing tests (append to config_test.go)**

Append to `internal/config/config_test.go`:
```go
func TestTelemetryIntervalDefault(t *testing.T) {
	c, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if c.TelemetryIntervalSeconds != 30 {
		t.Errorf("TelemetryIntervalSeconds default = %d, want 30", c.TelemetryIntervalSeconds)
	}
}

func TestTelemetryIntervalEnvOverride(t *testing.T) {
	t.Setenv("TELEMETRY_INTERVAL_SECONDS", "120")
	c, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if c.TelemetryIntervalSeconds != 120 {
		t.Errorf("TelemetryIntervalSeconds = %d, want 120", c.TelemetryIntervalSeconds)
	}
}

func TestTelemetryIntervalClamped(t *testing.T) {
	t.Setenv("TELEMETRY_INTERVAL_SECONDS", "0")
	c, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if c.TelemetryIntervalSeconds < 1 {
		t.Errorf("TelemetryIntervalSeconds = %d after clamp, want >= 1", c.TelemetryIntervalSeconds)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config/ -run 'TestTelemetryInterval' -v`
Expected: build failure — `c.TelemetryIntervalSeconds undefined`.

- [ ] **Step 3: Add the field, default, env hook, and clamp to config.go**

In `internal/config/config.go`, add the field to `Config`:
```go
type Config struct {
	HTTP                     HTTPConfig     `yaml:"http"`
	Device                   DeviceConfig   `yaml:"device"`
	Camera                   CameraConfig   `yaml:"camera"`
	SensorType               string         `yaml:"sensor_type"`
	Schedules                Schedules      `yaml:"schedules"`
	Water                    WaterConfig    `yaml:"water"`
	OverTemp                 OverTempConfig `yaml:"overtemp"`
	TelemetryIntervalSeconds int            `yaml:"telemetry_interval_seconds"`
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
		SensorType:               "AM2320",
		TelemetryIntervalSeconds: 30,
	}
}
```

In `applyEnv`, add the env hook (alongside the other `envInt` calls):
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
	envInt(&c.TelemetryIntervalSeconds, "TELEMETRY_INTERVAL_SECONDS")
}
```

In `Load` (after `applyEnv` and after the existing camera-interval clamp), add the telemetry clamp:
```go
func Load(path string) (Config, error) {
	c, err := LoadFileOnly(path)
	if err != nil {
		return Config{}, err
	}
	applyEnv(&c)
	if c.Camera.IntervalSeconds <= 0 {
		c.Camera.IntervalSeconds = 3600
	}
	if c.TelemetryIntervalSeconds < 1 {
		c.TelemetryIntervalSeconds = 1
	}
	return c, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/config/ -run 'TestTelemetryInterval' -v`
Expected:
```
--- PASS: TestTelemetryIntervalDefault (0.00s)
--- PASS: TestTelemetryIntervalEnvOverride (0.00s)
--- PASS: TestTelemetryIntervalClamped (0.00s)
PASS
```

- [ ] **Step 5: Run full config suite to confirm no regressions**

Run: `go test ./internal/config/ -v`
Expected: all existing tests PASS plus the three new ones.

---

### Task 2: State store — device identity fields + `SetDeviceInfo` setter

**Depends on:** none (within this plan; touches only `internal/state/`)

**Files:**
- Modify: `internal/state/state.go`
- Modify: `internal/state/state_test.go`

Add `Identifier`, `Model`, `Version` string fields to `Snapshot` (json tags `identifier`, `model`, `version`) and a `Store.SetDeviceInfo` setter. Wire in `main.go` (Task 5). The HA config flow reads `state["identifier"]` for `unique_id`.

- [ ] **Step 1: Write the failing test (append to state_test.go)**

Append to `internal/state/state_test.go`:
```go
func TestSetDeviceInfo(t *testing.T) {
	s := New()
	s.SetDeviceInfo("gardyn-42", "gardyn 3.0", "2.1.0")
	snap := s.Snapshot()
	if snap.Identifier != "gardyn-42" {
		t.Errorf("Identifier = %q, want %q", snap.Identifier, "gardyn-42")
	}
	if snap.Model != "gardyn 3.0" {
		t.Errorf("Model = %q, want %q", snap.Model, "gardyn 3.0")
	}
	if snap.Version != "2.1.0" {
		t.Errorf("Version = %q, want %q", snap.Version, "2.1.0")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/state/ -run TestSetDeviceInfo -v`
Expected: build failure — `s.SetDeviceInfo undefined`.

- [ ] **Step 3: Add fields and setter to state.go**

In `internal/state/state.go`, extend `Snapshot` with three new fields (add them after `OverTemp` and before `Schedules` for logical grouping):
```go
type Snapshot struct {
	Available  bool                 `json:"available"`
	UptimeS    int64                `json:"uptime_s"`
	Identifier string               `json:"identifier"`
	Model      string               `json:"model"`
	Version    string               `json:"version"`
	Light      LightState           `json:"light"`
	Pump       PumpState            `json:"pump"`
	Sensors    Sensors              `json:"sensors"`
	Water      WaterState           `json:"water"`
	OverTemp   bool                 `json:"overtemp"`
	Schedules  map[string]SchedFlag `json:"schedules"`
}
```

Add the setter below the existing `SetOverTemp` setter:
```go
// SetDeviceInfo records the device's unique identifier, model string, and
// firmware version in the snapshot. Call once at startup from main.
func (s *Store) SetDeviceInfo(identifier, model, version string) {
	s.mu.Lock()
	s.snap.Identifier = identifier
	s.snap.Model = model
	s.snap.Version = version
	s.mu.Unlock()
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/state/ -run TestSetDeviceInfo -v`
Expected:
```
--- PASS: TestSetDeviceInfo (0.00s)
PASS
```

- [ ] **Step 5: Run full state suite to confirm no regressions**

Run: `go test ./internal/state/ -v`
Expected: all existing tests PASS plus the new one.

---

### Task 3: httpapi — normalize error bodies to `{"error": ...}`

**Depends on:** none (within this plan; touches only `internal/httpapi/`)

**Files:**
- Modify: `internal/httpapi/httpapi.go`
- Modify: `internal/httpapi/httpapi_test.go`

`levelHandler` currently returns error responses with key `"message"` (e.g. `map[string]string{"message": "invalid JSON body"}`). Plan 3's routes all use `"error"`. Standardize ALL error responses on `"error"`. Success responses that legitimately use `"message"` (e.g. `{"message":"Light turned on"}`) are left untouched — they are success confirmations, not errors.

- [ ] **Step 1: Write the failing test (append to httpapi_test.go)**

Append to `internal/httpapi/httpapi_test.go`:
```go
func TestBadBrightnessBodyHasErrorKey(t *testing.T) {
	h, _, stop := newH(t)
	defer stop()

	// Bad value (out of range): previously returned {"message": "value must be 0..100"}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/light/brightness", strings.NewReader(`{"value":999}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if _, ok := body["error"]; !ok {
		t.Errorf("error body = %v, want key \"error\"", body)
	}
	if _, ok := body["message"]; ok {
		t.Errorf("error body still has legacy key \"message\": %v", body)
	}
}

func TestBadBrightnessInvalidJSONHasErrorKey(t *testing.T) {
	h, _, stop := newH(t)
	defer stop()

	// Malformed JSON: previously returned {"message": "invalid JSON body"}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/pump/speed", strings.NewReader(`not-json`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if _, ok := body["error"]; !ok {
		t.Errorf("error body = %v, want key \"error\"", body)
	}
}

func TestLightOnSuccessBodyUnchanged(t *testing.T) {
	h, _, stop := newH(t)
	defer stop()

	// Success response must still use "message", not "error"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/light/on", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if msg, ok := body["message"]; !ok || msg != "Light turned on" {
		t.Errorf("success body = %v, want {\"message\":\"Light turned on\"}", body)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/httpapi/ -run 'TestBadBrightnessBodyHasErrorKey|TestBadBrightnessInvalidJSONHasErrorKey' -v`
Expected:
```
--- FAIL: TestBadBrightnessBodyHasErrorKey (0.00s)
    httpapi_test.go:...: error body = map[message:value must be 0..100], want key "error"
--- FAIL: TestBadBrightnessInvalidJSONHasErrorKey (0.00s)
    httpapi_test.go:...: error body = map[message:invalid JSON body], want key "error"
FAIL
```

- [ ] **Step 3: Change the two error keys in levelHandler**

In `internal/httpapi/httpapi.go`, update `levelHandler` to use `"error"` instead of `"message"` in both error branches:
```go
func levelHandler(c *core.Core, target core.Target) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Value int `json:"value"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
			return
		}
		if body.Value < 0 || body.Value > 100 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "value must be 0..100"})
			return
		}
		c.Submit(core.Command{Target: target, Action: core.ActionSetLevel, Value: body.Value})
		writeJSON(w, http.StatusOK, map[string]int{"value": body.Value})
	}
}
```

(All success responses — `{"message":"Light turned on"}`, `{"message":"Pump turned on!"}`, etc. — remain untouched.)

- [ ] **Step 4: Run new tests to verify they pass**

Run: `go test ./internal/httpapi/ -run 'TestBadBrightness|TestLightOnSuccessBodyUnchanged' -v`
Expected:
```
--- PASS: TestBadBrightnessRejected (0.00s)
--- PASS: TestBadBrightnessBodyHasErrorKey (0.00s)
--- PASS: TestBadBrightnessInvalidJSONHasErrorKey (0.00s)
--- PASS: TestLightOnSuccessBodyUnchanged (0.00s)
PASS
```

- [ ] **Step 5: Run full httpapi suite to confirm no regressions**

Run: `go test ./internal/httpapi/ -v`
Expected: all existing tests PASS plus the three new ones.

---

### Task 4: Publisher — continuously recompute `water.low` each telemetry cycle

**Depends on:** none (within this plan; touches only `internal/publish/`)

**Files:**
- Modify: `internal/publish/publish.go`
- Modify: `internal/publish/publish_test.go`

Today `water.low` is only updated when the core's `applyPump` handles a pump-on command or when `SetWaterLowCM` is called at startup. This means the snapshot value can be stale between pump attempts. Fix: after reading Distance in `publishOnce`, check the snapshot for a non-zero threshold and recompute `low` via the existing `store.SetWater` setter.

**Concurrency note:** Both `core.applyPump` and `Publisher.publishOnce` call `store.SetWater`. This is safe because `SetWater` is mutex-guarded in the store. The periodic publisher provides the steady-state value; the interlock provides immediate feedback on a blocked pump-on. Both write the same field; there is no logical conflict — whichever write wins reflects the correct current measurement.

- [ ] **Step 1: Write the failing tests (append to publish_test.go)**

Append to `internal/publish/publish_test.go`:
```go
func TestPublishOnceRecomputesWaterLowAboveThreshold(t *testing.T) {
	devs := mock.New()
	// Default mock Distance returns 7.5 cm when CM == 0.
	// Set it explicitly above threshold: distance > threshold => water is low.
	devs.Distance.(*mock.Distance).CM = 15.0

	st := state.New()
	// Pre-set threshold of 10 cm: any distance > 10 means the reservoir is low.
	st.SetWater(10.0, false) // threshold=10, low=false (stale)

	p := New(devs, st, state.NewFrames(), 0, nil)
	p.publishOnce()

	snap := st.Snapshot()
	if !snap.Water.Low {
		t.Errorf("water.low = false, want true (distance 15 > threshold 10)")
	}
	if snap.Water.LowThresholdCM != 10.0 {
		t.Errorf("LowThresholdCM = %v, want 10.0", snap.Water.LowThresholdCM)
	}
}

func TestPublishOnceRecomputesWaterLowBelowThreshold(t *testing.T) {
	devs := mock.New()
	// Distance below threshold: distance <= threshold => water is OK.
	devs.Distance.(*mock.Distance).CM = 5.0

	st := state.New()
	// Pre-set threshold and stale low=true.
	st.SetWater(10.0, true) // threshold=10, low=true (stale)

	p := New(devs, st, state.NewFrames(), 0, nil)
	p.publishOnce()

	snap := st.Snapshot()
	if snap.Water.Low {
		t.Errorf("water.low = true, want false (distance 5 <= threshold 10)")
	}
}

func TestPublishOnceSkipsWaterLowWhenThresholdZero(t *testing.T) {
	devs := mock.New()
	devs.Distance.(*mock.Distance).CM = 20.0 // very large distance

	st := state.New()
	// Threshold of 0 means disabled — do not touch water.low.
	st.SetWater(0, false)

	p := New(devs, st, state.NewFrames(), 0, nil)
	p.publishOnce()

	snap := st.Snapshot()
	if snap.Water.Low {
		t.Errorf("water.low = true, want false (threshold disabled)")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/publish/ -run 'TestPublishOnceRecomputes|TestPublishOnceSkips' -v`
Expected:
```
--- FAIL: TestPublishOnceRecomputesWaterLowAboveThreshold (0.00s)
    publish_test.go:...: water.low = false, want true (distance 15 > threshold 10)
--- FAIL: TestPublishOnceRecomputesWaterLowBelowThreshold (0.00s)
    publish_test.go:...: water.low = true, want false (distance 5 <= threshold 10)
--- PASS: TestPublishOnceSkipsWaterLowWhenThresholdZero (0.00s)
PASS
```

(The skip-when-zero test may already pass accidentally; the two recompute tests must fail.)

- [ ] **Step 3: Extend publishOnce to recompute water.low**

In `internal/publish/publish.go`, update the Distance block in `publishOnce`:
```go
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
		if p.onOverTemp != nil {
			if over, err := p.dev.PCBTemp.OverTemp(); err == nil {
				p.onOverTemp(over)
			}
		}
	}
	if p.dev.Distance != nil {
		if cm, err := p.dev.Distance.MeasureCM(); err == nil {
			p.store.SetWaterLevel(cm)
			// Recompute water.low on every cycle so the snapshot stays fresh
			// even between pump-on attempts. Threshold == 0 means the interlock
			// is disabled; skip in that case so we don't overwrite a deliberate
			// SetWater call with a zero threshold.
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
		}
	}
}
```

- [ ] **Step 4: Run new tests to verify they pass**

Run: `go test ./internal/publish/ -run 'TestPublishOnceRecomputes|TestPublishOnceSkips' -v`
Expected:
```
--- PASS: TestPublishOnceRecomputesWaterLowAboveThreshold (0.00s)
--- PASS: TestPublishOnceRecomputesWaterLowBelowThreshold (0.00s)
--- PASS: TestPublishOnceSkipsWaterLowWhenThresholdZero (0.00s)
PASS
```

- [ ] **Step 5: Run full publish suite to confirm no regressions**

Run: `go test ./internal/publish/ -v`
Expected: all existing tests PASS plus the three new ones.

---

### Task 5: Wire in main.go + full suite + single commit

**Depends on:** Task 1 (TelemetryIntervalSeconds), Task 2 (SetDeviceInfo). Tasks 3 and 4 require no main changes.

**Files:**
- Modify: `cmd/gardynd/main.go`

Apply the two main.go wirings from Items A and B. Items C and D require no changes to `main.go` — `httpapi.go` and `publish.go` are self-contained.

- [ ] **Step 1: Wire SetDeviceInfo and TelemetryIntervalSeconds in main.go**

In `cmd/gardynd/main.go`, after `st := state.New()`, add the device-identity call:
```go
	st := state.New()
	st.SetDeviceInfo(cfg.Device.Identifier, cfg.Device.Model, cfg.Device.Version)
```

Then find the line:
```go
	pub := publish.New(devs, st, frames, 30*time.Minute, onOverTemp)
```
Replace it with:
```go
	pub := publish.New(devs, st, frames, time.Duration(cfg.TelemetryIntervalSeconds)*time.Second, onOverTemp)
```

No other changes are needed in main.go — Items C and D are fully self-contained in their respective packages.

After these edits, the relevant section of `cmd/gardynd/main.go` will look like:
```go
	st := state.New()
	st.SetDeviceInfo(cfg.Device.Identifier, cfg.Device.Model, cfg.Device.Version)
	c := core.New(devs, st)
	go c.Run()
	defer c.Stop()

	// ... (schedule/water wiring unchanged) ...

	frames := state.NewFrames()
	pub := publish.New(devs, st, frames, time.Duration(cfg.TelemetryIntervalSeconds)*time.Second, onOverTemp)
	go pub.Run()
	go pub.RunCameras(time.Duration(cfg.Camera.IntervalSeconds) * time.Second)
	defer pub.Stop()
```

- [ ] **Step 2: Build both targets**

Run: `make tidy && make build && make build-pi`
Expected: both `bin/gardynd` (host) and the Pi binary build with no errors.

- [ ] **Step 3: Run the full test suite with race detector**

Run: `go test ./... -race`
Expected: all packages PASS, no race conditions detected.

- [ ] **Step 4: Smoke-test the mock binary**

Run `./bin/gardynd --hw=mock` in one terminal, then in another:
```
curl -s http://localhost:5000/state | python3 -m json.tool
```
Confirm the JSON response contains `"identifier"`, `"model"`, and `"version"` fields at the top level.

Also confirm:
```
curl -s -X POST http://localhost:5000/light/brightness \
  -H 'Content-Type: application/json' \
  -d '{"value":999}' | python3 -m json.tool
```
Returns `{"error": "value must be 0..100"}` (not `{"message": ...}`).

- [ ] **Step 5: Commit (single commit covering all four items)**

```
git add internal/config/config.go internal/config/config_test.go \
        internal/state/state.go internal/state/state_test.go \
        internal/httpapi/httpapi.go internal/httpapi/httpapi_test.go \
        internal/publish/publish.go internal/publish/publish_test.go \
        cmd/gardynd/main.go
git commit -m "feat: configurable telemetry interval, device identity + fresh water.low in /state"
```

---

## Self-Review

**1. Spec coverage**

| Requirement | Task |
|---|---|
| Configurable telemetry interval (yaml, env, default 30, clamp ≥ 1) | Task 1 |
| Pass interval to `publish.New` in main | Task 5 |
| `Identifier`/`Model`/`Version` in `state.Snapshot` | Task 2 |
| `Store.SetDeviceInfo` setter | Task 2 |
| Wire `st.SetDeviceInfo` in main at startup | Task 5 |
| Normalize REST errors to `{"error": ...}` | Task 3 |
| Success bodies with `"message"` left untouched | Task 3 |
| Recompute `water.low` on every publisher cycle | Task 4 |
| Skip recompute when threshold == 0 | Task 4 |
| Single commit | Task 5, Step 5 |

No gaps identified.

**2. Placeholder scan**

No "TBD", "TODO", or "implement later" text in any task. Every code step contains complete, compilable Go. Every test step specifies the exact `go test` command and expected output. No cross-references that say "similar to Task N" without repeating the code.

**3. Type consistency**

- `cfg.TelemetryIntervalSeconds` (int) defined in Task 1, used as `time.Duration(cfg.TelemetryIntervalSeconds)*time.Second` in Task 5 — consistent with the identically-patterned `cfg.Camera.IntervalSeconds` usage already in main.
- `st.SetDeviceInfo(identifier, model, version string)` defined in Task 2, called with `cfg.Device.Identifier`, `cfg.Device.Model`, `cfg.Device.Version` in Task 5 — all three are `string` fields in `DeviceConfig`, which already exists in `config.go`.
- `snap.Water.LowThresholdCM` and `snap.Water.Low` used in Task 4's `publishOnce` — both are existing fields of `WaterState` (from `internal/state/state.go` line 34–37). `p.store.SetWater(threshold, low)` matches the existing `func (s *Store) SetWater(thresholdCM float64, low bool)` signature.
- `publish.New(devs, st, frames, interval, onOverTemp)` — 5-argument signature from Plan 3's `internal/publish/publish.go` line 24. Task 5 keeps the same call site, only changing the interval argument.
- `map[string]string{"error": ...}` in Task 3 matches what `HandlerFull` and `sensorMux` already use for their error responses.

All types, method signatures, and field names are consistent across tasks and with the existing source.

**4. Dependency audit**

- **Task 1** (`internal/config/`): No other task in this plan defines or modifies config files. Depends on Plan 1–3 existing. Marked `none` within this plan — correct.
- **Task 2** (`internal/state/`): No other task in this plan defines or modifies state files. Depends on Plan 1–3 existing. Marked `none` within this plan — correct.
- **Task 3** (`internal/httpapi/`): No other task in this plan defines or modifies httpapi files. Depends on Plans 1–3 existing. Marked `none` within this plan — correct.
- **Task 4** (`internal/publish/`): No other task in this plan defines or modifies publish files. Depends on Plan 3 (`publish.go` and `state.Store.SetWater`). Marked `none` within this plan — correct (Plan 3 is a prerequisite of the whole plan, not a peer task).
- **Task 5** (`cmd/gardynd/main.go`): reads `cfg.TelemetryIntervalSeconds` (Task 1) and calls `st.SetDeviceInfo` (Task 2). Must run after Tasks 1 and 2. Tasks 3 and 4 are self-contained and do not require main changes. Marked `Depends on: Task 1, Task 2` — correct.
- Tasks 1, 2, 3, 4 are mutually independent (disjoint file sets, no shared interfaces introduced in this plan). They can be executed in parallel.
