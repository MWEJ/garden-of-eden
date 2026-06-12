# gardynd Plan 1 — Skeleton & Vertical Slice Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a runnable REST-only `gardynd` Go binary that controls a (mock) light and pump and serves a `GET /state` snapshot — the skeleton every later plan extends.

**Architecture:** A single-writer `core` goroutine owns all device state; REST handlers submit commands over a channel. After applying each command the core writes values into a thread-safe snapshot store that `GET /state` serializes. Hardware sits behind interfaces with an in-memory mock so the whole service runs on a laptop via `--hw=mock`. No MQTT — Home Assistant connects later through the dedicated integration that polls this API.

**Tech Stack:** Go (ARMv6 target, `CGO_ENABLED=0`), `gopkg.in/yaml.v3`, stdlib `net/http`.

**Spec:** `docs/superpowers/specs/2026-06-12-gardynd-go-service-design.md`

---

## Project File Structure (whole service; this plan builds the **bold** files)

```
go.mod / go.sum
Makefile
**cmd/gardynd/main.go**            wiring + flags
**internal/config/config.go**      YAML load + env overrides
**internal/config/config_test.go**
**internal/hw/hw.go**              driver interfaces (Light, Pump, + later sensors)
**internal/hw/mock/mock.go**       in-memory fakes
**internal/hw/mock/mock_test.go**
**internal/state/state.go**        thread-safe snapshot store
**internal/state/state_test.go**
**internal/core/core.go**          single-writer state machine
**internal/core/core_test.go**
**internal/httpapi/httpapi.go**    REST handlers (control + /state + /healthz)
**internal/httpapi/httpapi_test.go**
 internal/hw/real/...              (Plan 2: real drivers)
 internal/core/scheduler.go        (Plan 3)
```

Module path: `github.com/iot-root/garden-of-eden`.

---

### Task 1: Go module, directory skeleton, Makefile

**Depends on:** none

**Files:**
- Create: `go.mod`
- Create: `Makefile`
- Create: `cmd/gardynd/main.go` (temporary stub, replaced in Task 7)

- [ ] **Step 1: Initialize the module**

Run:
```
go mod init github.com/iot-root/garden-of-eden
go get gopkg.in/yaml.v3@v3.0.1
```
Expected: `go.mod` created with the yaml require.

- [ ] **Step 2: Create a temporary main so the module builds**

`cmd/gardynd/main.go`:
```go
package main

func main() {}
```

- [ ] **Step 3: Create the Makefile**

`Makefile`:
```make
BINARY := gardynd
PKG := ./cmd/gardynd

.PHONY: build build-pi test tidy

build:
	go build -o bin/$(BINARY) $(PKG)

# Stock Pi Zero is ARMv6, 32-bit. CGO off for a static binary.
build-pi:
	GOOS=linux GOARCH=arm GOARM=6 CGO_ENABLED=0 go build -trimpath -o bin/$(BINARY)-armv6 $(PKG)

test:
	go test ./...

tidy:
	go mod tidy
```

- [ ] **Step 4: Verify it builds**

Run: `make build`
Expected: `bin/gardynd` produced, no errors.

---

### Task 2: Config package

**Depends on:** Task 1

**Files:**
- Create: `internal/config/config.go`
- Test: `internal/config/config_test.go`

Config loads from a YAML file, then applies environment-variable overrides so
the existing `.env` keys keep working during migration (parity with
`config.py`). No MQTT credentials — those are gone.

- [ ] **Step 1: Write the failing test**

`internal/config/config_test.go`:
```go
package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDefaults(t *testing.T) {
	c, err := Load("")
	if err != nil {
		t.Fatalf("Load(\"\"): %v", err)
	}
	if c.HTTP.Port != 5000 {
		t.Errorf("http port default = %d", c.HTTP.Port)
	}
	if c.Device.Identifier != "gardyn-xx" {
		t.Errorf("identifier default = %q", c.Device.Identifier)
	}
}

func TestFileThenEnvOverride(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	yaml := "http:\n  port: 5050\ndevice:\n  identifier: gardyn-01\n"
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MQTT_IDENTIFIER", "gardyn-env") // legacy env key still recognized

	c, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.HTTP.Port != 5050 { // file wins over default
		t.Errorf("port = %d, want 5050", c.HTTP.Port)
	}
	if c.Device.Identifier != "gardyn-env" { // env wins over file
		t.Errorf("identifier = %q, want gardyn-env", c.Device.Identifier)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config/ -run TestLoad -v`
Expected: build failure — `undefined: Load`.

- [ ] **Step 3: Write the implementation**

`internal/config/config.go`:
```go
// Package config loads gardynd configuration from an optional YAML file,
// then applies environment-variable overrides for .env compatibility.
package config

import (
	"fmt"
	"os"
	"strconv"

	"gopkg.in/yaml.v3"
)

type HTTPConfig struct {
	Port int `yaml:"port"`
}

type DeviceConfig struct {
	Identifier string `yaml:"identifier"`
	Model      string `yaml:"model"`
	Version    string `yaml:"version"`
}

type Config struct {
	HTTP   HTTPConfig   `yaml:"http"`
	Device DeviceConfig `yaml:"device"`
}

func defaults() Config {
	return Config{
		HTTP:   HTTPConfig{Port: 5000},
		Device: DeviceConfig{Identifier: "gardyn-xx", Model: "gardyn 3.0", Version: "1.0.0"},
	}
}

// Load reads defaults, overlays the YAML file at path (if non-empty), then
// applies environment-variable overrides.
func Load(path string) (Config, error) {
	c := defaults()
	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return Config{}, fmt.Errorf("read config: %w", err)
		}
		if err := yaml.Unmarshal(data, &c); err != nil {
			return Config{}, fmt.Errorf("parse config: %w", err)
		}
	}
	applyEnv(&c)
	return c, nil
}

func applyEnv(c *Config) {
	envInt(&c.HTTP.Port, "HTTP_PORT")
	envStr(&c.Device.Identifier, "MQTT_IDENTIFIER") // legacy key name retained
	envStr(&c.Device.Model, "MQTT_DEVICE_MODEL")
	envStr(&c.Device.Version, "MQTT_VERSION")
}

func envStr(dst *string, key string) {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		*dst = v
	}
}

func envInt(dst *int, key string) {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			*dst = n
		}
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/config/ -v`
Expected: PASS.

---

### Task 3: Hardware interfaces + in-memory mock

**Depends on:** Task 1

**Files:**
- Create: `internal/hw/hw.go`
- Create: `internal/hw/mock/mock.go`
- Test: `internal/hw/mock/mock_test.go`

- [ ] **Step 1: Define the interfaces**

`internal/hw/hw.go`:
```go
// Package hw defines hardware driver interfaces. Real implementations land in
// Plan 2; an in-memory mock lives under hw/mock.
package hw

// Light is a dimmable PWM light, brightness 0..100 percent.
type Light interface {
	SetBrightness(pct int) error
	Brightness() int
	Off() error
}

// Pump is a PWM pump, speed 0..100 percent.
type Pump interface {
	SetSpeed(pct int) error
	Speed() int
	Off() error
}

// Devices bundles the hardware the core controls. Later plans add sensor fields.
type Devices struct {
	Light Light
	Pump  Pump
}
```

- [ ] **Step 2: Write the failing mock test**

`internal/hw/mock/mock_test.go`:
```go
package mock

import "testing"

func TestLightBrightnessClamp(t *testing.T) {
	l := &Light{}
	if err := l.SetBrightness(70); err != nil {
		t.Fatal(err)
	}
	if l.Brightness() != 70 {
		t.Errorf("brightness = %d, want 70", l.Brightness())
	}
	if err := l.SetBrightness(150); err == nil {
		t.Error("expected error for out-of-range brightness")
	}
	if err := l.Off(); err != nil {
		t.Fatal(err)
	}
	if l.Brightness() != 0 {
		t.Errorf("after Off brightness = %d, want 0", l.Brightness())
	}
}

func TestPumpSpeed(t *testing.T) {
	p := &Pump{}
	if err := p.SetSpeed(40); err != nil {
		t.Fatal(err)
	}
	if p.Speed() != 40 {
		t.Errorf("speed = %d, want 40", p.Speed())
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/hw/mock/ -v`
Expected: build failure — `undefined: Light`.

- [ ] **Step 4: Implement the mock**

`internal/hw/mock/mock.go`:
```go
// Package mock provides in-memory fakes of the hw interfaces for tests and
// for running gardynd on a laptop via --hw=mock.
package mock

import (
	"fmt"
	"sync"

	"github.com/iot-root/garden-of-eden/internal/hw"
)

type Light struct {
	mu  sync.Mutex
	pct int
}

func (l *Light) SetBrightness(pct int) error {
	if pct < 0 || pct > 100 {
		return fmt.Errorf("brightness %d out of range 0..100", pct)
	}
	l.mu.Lock()
	l.pct = pct
	l.mu.Unlock()
	return nil
}

func (l *Light) Brightness() int { l.mu.Lock(); defer l.mu.Unlock(); return l.pct }
func (l *Light) Off() error      { return l.SetBrightness(0) }

type Pump struct {
	mu  sync.Mutex
	pct int
}

func (p *Pump) SetSpeed(pct int) error {
	if pct < 0 || pct > 100 {
		return fmt.Errorf("speed %d out of range 0..100", pct)
	}
	p.mu.Lock()
	p.pct = pct
	p.mu.Unlock()
	return nil
}

func (p *Pump) Speed() int { p.mu.Lock(); defer p.mu.Unlock(); return p.pct }
func (p *Pump) Off() error { return p.SetSpeed(0) }

// New returns a Devices bundle backed by mocks.
func New() hw.Devices {
	return hw.Devices{Light: &Light{}, Pump: &Pump{}}
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/hw/mock/ -v`
Expected: PASS.

---

### Task 4: Snapshot store

**Depends on:** Task 1

**Files:**
- Create: `internal/state/state.go`
- Test: `internal/state/state_test.go`

A thread-safe store the core writes to and `GET /state` serializes. Fields are
pointers where "absent" must be distinguishable from zero (sensors), so the HA
integration can map `null` to unavailable.

- [ ] **Step 1: Write the failing test**

`internal/state/state_test.go`:
```go
package state

import (
	"encoding/json"
	"testing"
)

func TestStoreSnapshotJSON(t *testing.T) {
	s := New()
	s.SetLight(true, 70)
	s.SetPump(false, 100)

	b, err := json.Marshal(s.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	light := got["light"].(map[string]any)
	if light["on"] != true || light["brightness"].(float64) != 70 {
		t.Errorf("light = %v", light)
	}
	if got["available"] != true {
		t.Errorf("available = %v", got["available"])
	}
}

func TestConcurrentWrites(t *testing.T) {
	s := New()
	done := make(chan struct{})
	go func() {
		for i := 0; i < 1000; i++ {
			s.SetLight(true, i%101)
		}
		close(done)
	}()
	for i := 0; i < 1000; i++ {
		_ = s.Snapshot()
	}
	<-done
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/state/ -v`
Expected: build failure — `undefined: New`.

- [ ] **Step 3: Implement the store**

`internal/state/state.go`:
```go
// Package state holds the thread-safe device snapshot served by GET /state.
package state

import (
	"sync"
	"time"
)

type LightState struct {
	On         bool `json:"on"`
	Brightness int  `json:"brightness"`
}

type PumpState struct {
	On    bool `json:"on"`
	Speed int  `json:"speed"`
}

type PumpPower struct {
	BusVoltage float64 `json:"bus_voltage"`
	Current    float64 `json:"current"`
	Power      float64 `json:"power"`
}

// Sensors uses pointers so an absent/failed sensor serializes as null.
type Sensors struct {
	TemperatureC *float64   `json:"temperature_c"`
	HumidityPct  *float64   `json:"humidity_pct"`
	PCBTempC     *float64   `json:"pcb_temp_c"`
	WaterLevelCM *float64   `json:"water_level_cm"`
	Pump         *PumpPower `json:"pump"`
}

type WaterState struct {
	LowThresholdCM float64 `json:"low_threshold_cm"`
	Low            bool    `json:"low"`
}

type Snapshot struct {
	Available bool                  `json:"available"`
	UptimeS   int64                 `json:"uptime_s"`
	Light     LightState            `json:"light"`
	Pump      PumpState             `json:"pump"`
	Sensors   Sensors               `json:"sensors"`
	Water     WaterState            `json:"water"`
	OverTemp  bool                  `json:"overtemp"`
	Schedules map[string]SchedFlag  `json:"schedules"`
}

type SchedFlag struct {
	Enabled bool `json:"enabled"`
}

type Store struct {
	mu    sync.RWMutex
	start time.Time
	snap  Snapshot
}

func New() *Store {
	return &Store{
		start: time.Now(),
		snap: Snapshot{
			Available: true,
			Schedules: map[string]SchedFlag{"light": {}, "pump": {}},
		},
	}
}

// Snapshot returns a copy with a freshly-computed uptime.
func (s *Store) Snapshot() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	snap := s.snap
	snap.UptimeS = int64(time.Since(s.start).Seconds())
	return snap
}

func (s *Store) SetLight(on bool, brightness int) {
	s.mu.Lock()
	s.snap.Light = LightState{On: on, Brightness: brightness}
	s.mu.Unlock()
}

func (s *Store) SetPump(on bool, speed int) {
	s.mu.Lock()
	s.snap.Pump = PumpState{On: on, Speed: speed}
	s.mu.Unlock()
}

func (s *Store) SetWater(thresholdCM float64, low bool) {
	s.mu.Lock()
	s.snap.Water = WaterState{LowThresholdCM: thresholdCM, Low: low}
	s.mu.Unlock()
}

func (s *Store) SetOverTemp(v bool) { s.mu.Lock(); s.snap.OverTemp = v; s.mu.Unlock() }

func (s *Store) SetScheduleEnabled(channel string, enabled bool) {
	s.mu.Lock()
	s.snap.Schedules[channel] = SchedFlag{Enabled: enabled}
	s.mu.Unlock()
}

// Sensor setters (used by Plan 3 publishers).
func (s *Store) SetTemperature(c float64)  { s.mu.Lock(); s.snap.Sensors.TemperatureC = &c; s.mu.Unlock() }
func (s *Store) SetHumidity(p float64)     { s.mu.Lock(); s.snap.Sensors.HumidityPct = &p; s.mu.Unlock() }
func (s *Store) SetPCBTemp(c float64)      { s.mu.Lock(); s.snap.Sensors.PCBTempC = &c; s.mu.Unlock() }
func (s *Store) SetWaterLevel(cm float64)  { s.mu.Lock(); s.snap.Sensors.WaterLevelCM = &cm; s.mu.Unlock() }
func (s *Store) SetPumpPower(p PumpPower)  { s.mu.Lock(); s.snap.Sensors.Pump = &p; s.mu.Unlock() }
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/state/ -race -v`
Expected: PASS (no data races).

---

### Task 5: Core single-writer state machine

**Depends on:** Task 3 (`hw.Devices`), Task 4 (`*state.Store`)

**Files:**
- Create: `internal/core/core.go`
- Test: `internal/core/core_test.go`

The core owns all mutation. Inputs submit `Command`s; the core applies them to
hardware and writes results into the snapshot store.

- [ ] **Step 1: Write the failing test**

`internal/core/core_test.go`:
```go
package core

import (
	"testing"
	"time"

	"github.com/iot-root/garden-of-eden/internal/hw/mock"
	"github.com/iot-root/garden-of-eden/internal/state"
)

func waitLight(st *state.Store, want int) bool {
	for i := 0; i < 50; i++ {
		if st.Snapshot().Light.Brightness == want {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

func TestLightOnUpdatesSnapshot(t *testing.T) {
	st := state.New()
	c := New(mock.New(), st)
	go c.Run()
	defer c.Stop()

	c.Submit(Command{Target: TargetLight, Action: ActionOn, Value: 70})

	if !waitLight(st, 70) {
		t.Errorf("light brightness not 70; snapshot=%+v", st.Snapshot().Light)
	}
	if !st.Snapshot().Light.On {
		t.Error("light.on not true")
	}
}

func TestPumpOffUpdatesSnapshot(t *testing.T) {
	st := state.New()
	c := New(mock.New(), st)
	go c.Run()
	defer c.Stop()

	c.Submit(Command{Target: TargetPump, Action: ActionOff})
	for i := 0; i < 50; i++ {
		if !st.Snapshot().Pump.On {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Error("pump.on stayed true")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/core/ -v`
Expected: build failure — `undefined: New`.

- [ ] **Step 3: Implement the core**

`internal/core/core.go`:
```go
// Package core is the single-writer state machine. All hardware mutation goes
// through one goroutine; inputs submit Commands and the core writes results to
// the snapshot store.
package core

import (
	"log"

	"github.com/iot-root/garden-of-eden/internal/hw"
	"github.com/iot-root/garden-of-eden/internal/state"
)

type Target int

const (
	TargetLight Target = iota
	TargetPump
)

type Action int

const (
	ActionOn Action = iota
	ActionOff
	ActionSetLevel // Value is the new brightness/speed percent
)

type Command struct {
	Target Target
	Action Action
	Value  int
}

type Core struct {
	dev        hw.Devices
	store      *state.Store
	cmds       chan Command
	done       chan struct{}
	lightLevel int
	pumpLevel  int
}

func New(dev hw.Devices, store *state.Store) *Core {
	return &Core{
		dev:        dev,
		store:      store,
		cmds:       make(chan Command, 16),
		done:       make(chan struct{}),
		lightLevel: 50,
		pumpLevel:  100,
	}
}

func (c *Core) Submit(cmd Command) { c.cmds <- cmd }
func (c *Core) Stop()              { close(c.done) }

func (c *Core) Run() {
	for {
		select {
		case <-c.done:
			return
		case cmd := <-c.cmds:
			c.apply(cmd)
		}
	}
}

func (c *Core) apply(cmd Command) {
	switch cmd.Target {
	case TargetLight:
		c.applyLight(cmd)
	case TargetPump:
		c.applyPump(cmd)
	}
}

func (c *Core) applyLight(cmd Command) {
	switch cmd.Action {
	case ActionOn:
		if cmd.Value > 0 {
			c.lightLevel = cmd.Value
		}
		if err := c.dev.Light.SetBrightness(c.lightLevel); err != nil {
			log.Printf("light on: %v", err)
			return
		}
		c.store.SetLight(true, c.lightLevel)
	case ActionOff:
		if err := c.dev.Light.Off(); err != nil {
			log.Printf("light off: %v", err)
			return
		}
		c.store.SetLight(false, c.lightLevel)
	case ActionSetLevel:
		c.lightLevel = cmd.Value
		if err := c.dev.Light.SetBrightness(cmd.Value); err != nil {
			log.Printf("light level: %v", err)
			return
		}
		c.store.SetLight(cmd.Value > 0, cmd.Value)
	}
}

func (c *Core) applyPump(cmd Command) {
	switch cmd.Action {
	case ActionOn:
		if cmd.Value > 0 {
			c.pumpLevel = cmd.Value
		}
		if err := c.dev.Pump.SetSpeed(c.pumpLevel); err != nil {
			log.Printf("pump on: %v", err)
			return
		}
		c.store.SetPump(true, c.pumpLevel)
	case ActionOff:
		if err := c.dev.Pump.Off(); err != nil {
			log.Printf("pump off: %v", err)
			return
		}
		c.store.SetPump(false, c.pumpLevel)
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

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/core/ -race -v`
Expected: PASS.

---

### Task 6: REST API — control + `/state` + `/healthz`

**Depends on:** Task 5 (core), Task 4 (store)

**Files:**
- Create: `internal/httpapi/httpapi.go`
- Test: `internal/httpapi/httpapi_test.go`

- [ ] **Step 1: Write the failing test**

`internal/httpapi/httpapi_test.go`:
```go
package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/iot-root/garden-of-eden/internal/core"
	"github.com/iot-root/garden-of-eden/internal/hw/mock"
	"github.com/iot-root/garden-of-eden/internal/state"
)

func newH(t *testing.T) (http.Handler, *state.Store, func()) {
	st := state.New()
	c := core.New(mock.New(), st)
	go c.Run()
	return Handler(c, st), st, c.Stop
}

func TestLightOnThenState(t *testing.T) {
	h, st, stop := newH(t)
	defer stop()

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/light/brightness", strings.NewReader(`{"value":42}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("brightness status = %d", rec.Code)
	}

	// Poll /state until the command is applied.
	var snap state.Snapshot
	for i := 0; i < 50; i++ {
		rec = httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/state", nil))
		_ = json.Unmarshal(rec.Body.Bytes(), &snap)
		if snap.Light.Brightness == 42 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if snap.Light.Brightness != 42 {
		t.Errorf("state light brightness = %d, want 42", snap.Light.Brightness)
	}
	_ = st
}

func TestHealthz(t *testing.T) {
	h, _, stop := newH(t)
	defer stop()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("healthz status = %d", rec.Code)
	}
}

func TestBadBrightnessRejected(t *testing.T) {
	h, _, stop := newH(t)
	defer stop()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/light/brightness", strings.NewReader(`{"value":150}`)))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/httpapi/ -v`
Expected: build failure — `undefined: Handler`.

- [ ] **Step 3: Implement the handlers**

`internal/httpapi/httpapi.go`:
```go
// Package httpapi exposes the REST control + state surface, submitting commands
// to the core and serving the snapshot store.
package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/iot-root/garden-of-eden/internal/core"
	"github.com/iot-root/garden-of-eden/internal/state"
)

// Handler builds the REST mux. Plan 3 extends it (schedules, sensors, cameras)
// via baseMux.
func Handler(c *core.Core, st *state.Store) http.Handler { return baseMux(c, st) }

func baseMux(c *core.Core, st *state.Store) *http.ServeMux {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /state", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, st.Snapshot())
	})
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

func levelHandler(c *core.Core, target core.Target) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Value int `json:"value"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"message": "invalid JSON body"})
			return
		}
		if body.Value < 0 || body.Value > 100 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"message": "value must be 0..100"})
			return
		}
		c.Submit(core.Command{Target: target, Action: core.ActionSetLevel, Value: body.Value})
		writeJSON(w, http.StatusOK, map[string]int{"value": body.Value})
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/httpapi/ -v`
Expected: PASS.

---

### Task 7: Wire `main`, run full suite, single commit

**Depends on:** Tasks 2, 3, 4, 5, 6

**Files:**
- Modify: `cmd/gardynd/main.go` (replace the Task 1 stub)

- [ ] **Step 1: Implement main**

`cmd/gardynd/main.go`:
```go
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/iot-root/garden-of-eden/internal/config"
	"github.com/iot-root/garden-of-eden/internal/core"
	"github.com/iot-root/garden-of-eden/internal/httpapi"
	"github.com/iot-root/garden-of-eden/internal/hw"
	"github.com/iot-root/garden-of-eden/internal/hw/mock"
	"github.com/iot-root/garden-of-eden/internal/state"
)

func main() {
	configPath := flag.String("config", "", "path to YAML config file")
	hwMode := flag.String("hw", "real", "hardware backend: real|mock")
	httpPort := flag.Int("http-port", 0, "override HTTP port (0 = use config)")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	if *httpPort != 0 {
		cfg.HTTP.Port = *httpPort
	}

	var devs hw.Devices
	switch *hwMode {
	case "mock":
		devs = mock.New()
	case "real":
		log.Fatalf("real hardware backend not implemented until Plan 2; run with --hw=mock")
	default:
		log.Fatalf("unknown --hw value %q", *hwMode)
	}

	st := state.New()
	c := core.New(devs, st)
	go c.Run()
	defer c.Stop()

	addr := fmt.Sprintf(":%d", cfg.HTTP.Port)
	server := &http.Server{Addr: addr, Handler: httpapi.Handler(c, st)}
	go func() {
		log.Printf("REST listening on %s", addr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("http: %v", err)
		}
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	log.Printf("shutting down")
}
```

- [ ] **Step 2: Tidy, build (host + Pi), full suite**

Run: `make tidy && make build && make build-pi && go test ./... -race`
Expected: both binaries build; all tests PASS, no races.

- [ ] **Step 3: Manual smoke (optional)**

Run `./bin/gardynd --hw=mock`, then:
`curl -X POST localhost:5000/light/brightness -d '{"value":70}'` and
`curl localhost:5000/state` — confirm `light.brightness` is 70.

- [ ] **Step 4: Commit (single commit for the whole plan)**

Run:
```
git add go.mod go.sum Makefile cmd/ internal/
git commit -m "feat: gardynd skeleton — REST control + /state snapshot (light, pump)"
```

---

## Self-Review

**Spec coverage (Plan 1 portion):** single binary scaffold ✓; mock backend /
`--hw=mock` laptop dev loop ✓; single-writer core ✓; snapshot store + `GET /state`
✓; REST control for light+pump ✓; `/healthz` ✓; ARMv6 cross-compile ✓; no MQTT
✓. Deferred by design: real drivers (Plan 2); scheduler, interlocks, sensors,
publishers, `/schedules`, `/camera`, zeroconf (Plan 3); systemd/CI/README/Python
removal (Plan 4). No Plan-1 requirement is unaddressed.

**Placeholder scan:** None. The `case "real"` fatal is an intentional guard until
Plan 2, not a placeholder.

**Type consistency:** `hw.Devices{Light, Pump}`, `state.New() *Store` with
`SetLight/SetPump/Snapshot`, `core.New(hw.Devices, *state.Store)`,
`core.Command{Target, Action, Value}`, `httpapi.Handler(*core.Core, *state.Store)`
+ `baseMux` are used consistently across tasks and main. The snapshot JSON shape
matches the spec §4.1.

**Dependency audit:** Task 1 (none) creates the module/stub. Tasks 2, 3, 4 depend
only on Task 1 and touch disjoint packages (`config`, `hw`, `state`) → safe to
parallelize. Task 5 depends on Tasks 3+4 (uses both). Task 6 depends on Tasks
4+5. Task 7 modifies `cmd/gardynd/main.go` (from Task 1) and imports every
package → depends on all. No `Depends on: none` task shares files with another.
Audit clean.
