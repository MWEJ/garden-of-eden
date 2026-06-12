# gardynd Plan 1 — Skeleton & Vertical Slice Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a runnable `gardynd` Go binary that controls a (mock) light and pump end-to-end over both REST and MQTT, publishing Home Assistant discovery + availability — the skeleton every later plan extends.

**Architecture:** A single-writer `core` goroutine owns all device state; MQTT and REST handlers submit commands over a channel. Hardware sits behind interfaces with an in-memory mock so the whole service runs on a laptop via `--hw=mock`. MQTT uses HA MQTT discovery with a Last-Will availability topic.

**Tech Stack:** Go (ARMv6 target, `CGO_ENABLED=0`), `gopkg.in/yaml.v3`, `github.com/eclipse/paho.mqtt.golang`, `github.com/mochi-mqtt/server` (test-only embedded broker), stdlib `net/http`.

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
**internal/core/core.go**          single-writer state machine
**internal/core/core_test.go**
**internal/mqttsvc/discovery.go**  HA discovery payload builders
**internal/mqttsvc/discovery_test.go**
**internal/mqttsvc/mqttsvc.go**    paho client, subscribe/dispatch, publish, LWT
**internal/mqttsvc/mqttsvc_test.go**
**internal/httpapi/httpapi.go**    REST handlers
**internal/httpapi/httpapi_test.go**
 internal/hw/light.go ...          (Plan 2: real drivers)
 internal/core/scheduler.go        (Plan 3)
```

Module path: `github.com/iot-root/garden-of-eden`.

---

### Task 1: Go module, directory skeleton, Makefile

**Depends on:** none

**Files:**
- Create: `go.mod`
- Create: `Makefile`
- Create: `cmd/gardynd/main.go` (temporary stub, replaced in Task 8)

- [ ] **Step 1: Initialize the module**

Run:
```
go mod init github.com/iot-root/garden-of-eden
go get gopkg.in/yaml.v3@v3.0.1
go get github.com/eclipse/paho.mqtt.golang@v1.5.0
go get github.com/mochi-mqtt/server/v2@v2.6.6
```
Expected: `go.mod` created and the three requires added.

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
`config.py`).

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
	if c.MQTT.Broker != "localhost" || c.MQTT.Port != 1883 {
		t.Errorf("broker defaults = %s:%d", c.MQTT.Broker, c.MQTT.Port)
	}
	if c.Device.BaseTopic != "gardyn" || c.Device.Identifier != "gardyn-xx" {
		t.Errorf("device defaults = %q / %q", c.Device.BaseTopic, c.Device.Identifier)
	}
	if c.HTTP.Port != 5000 {
		t.Errorf("http port default = %d", c.HTTP.Port)
	}
}

func TestFileThenEnvOverride(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	yaml := "mqtt:\n  broker: filebroker\n  port: 1900\ndevice:\n  identifier: gardyn-01\n"
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MQTT_BROKER", "envbroker")
	t.Setenv("MQTT_BASETOPIC", "garden2")

	c, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.MQTT.Broker != "envbroker" { // env wins over file
		t.Errorf("broker = %q, want envbroker", c.MQTT.Broker)
	}
	if c.MQTT.Port != 1900 { // file wins over default
		t.Errorf("port = %d, want 1900", c.MQTT.Port)
	}
	if c.Device.Identifier != "gardyn-01" {
		t.Errorf("identifier = %q, want gardyn-01", c.Device.Identifier)
	}
	if c.Device.BaseTopic != "garden2" {
		t.Errorf("base topic = %q, want garden2", c.Device.BaseTopic)
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

type MQTTConfig struct {
	Broker    string `yaml:"broker"`
	Port      int    `yaml:"port"`
	Username  string `yaml:"username"`
	Password  string `yaml:"password"`
	KeepAlive int    `yaml:"keepalive"`
}

type HTTPConfig struct {
	Port int `yaml:"port"`
}

type DeviceConfig struct {
	BaseTopic  string `yaml:"base_topic"`
	Identifier string `yaml:"identifier"`
	Model      string `yaml:"model"`
	Version    string `yaml:"version"`
}

type Config struct {
	MQTT   MQTTConfig   `yaml:"mqtt"`
	HTTP   HTTPConfig   `yaml:"http"`
	Device DeviceConfig `yaml:"device"`
}

func defaults() Config {
	return Config{
		MQTT:   MQTTConfig{Broker: "localhost", Port: 1883, KeepAlive: 60},
		HTTP:   HTTPConfig{Port: 5000},
		Device: DeviceConfig{BaseTopic: "gardyn", Identifier: "gardyn-xx", Model: "gardyn 3.0", Version: "1.0.0"},
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
	envStr(&c.MQTT.Broker, "MQTT_BROKER")
	envInt(&c.MQTT.Port, "MQTT_PORT")
	envInt(&c.MQTT.KeepAlive, "MQTT_KEEPALIVE_INTERVAL")
	envStr(&c.MQTT.Username, "MQTT_USERNAME")
	envStr(&c.MQTT.Password, "MQTT_PASSWORD")
	envStr(&c.Device.BaseTopic, "MQTT_BASETOPIC")
	envStr(&c.Device.Identifier, "MQTT_IDENTIFIER")
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

// Devices bundles the hardware the core controls. Later plans add sensor
// fields (distance, env, pcb temp, power, camera, button).
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

### Task 4: Core single-writer state machine

**Depends on:** Task 3 (consumes `hw.Devices`)

**Files:**
- Create: `internal/core/core.go`
- Test: `internal/core/core_test.go`

The core owns all state. Inputs submit `Command`s; the core applies them to
hardware, updates state, and emits `StateChange` events through a publish
callback that MQTT/other consumers subscribe to.

- [ ] **Step 1: Write the failing test**

`internal/core/core_test.go`:
```go
package core

import (
	"testing"
	"time"

	"github.com/iot-root/garden-of-eden/internal/hw/mock"
)

func drain(ch <-chan StateChange, n int, d time.Duration) []StateChange {
	var out []StateChange
	deadline := time.After(d)
	for len(out) < n {
		select {
		case s := <-ch:
			out = append(out, s)
		case <-deadline:
			return out
		}
	}
	return out
}

func TestLightOnPublishesState(t *testing.T) {
	c := New(mock.New())
	events := c.Subscribe()
	go c.Run()
	defer c.Stop()

	c.Submit(Command{Target: TargetLight, Action: ActionOn, Value: 70})

	got := drain(events, 2, time.Second)
	want := map[string]string{"light/state": "ON", "light/brightness/state": "70"}
	seen := map[string]string{}
	for _, s := range got {
		seen[s.Topic] = s.Payload
	}
	for k, v := range want {
		if seen[k] != v {
			t.Errorf("event %q = %q, want %q (all: %v)", k, seen[k], v, seen)
		}
	}
}

func TestPumpOffPublishesState(t *testing.T) {
	c := New(mock.New())
	events := c.Subscribe()
	go c.Run()
	defer c.Stop()

	c.Submit(Command{Target: TargetPump, Action: ActionOff})
	got := drain(events, 1, time.Second)
	if len(got) == 0 || got[0].Topic != "pump/state" || got[0].Payload != "OFF" {
		t.Errorf("got %v, want pump/state OFF", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/core/ -v`
Expected: build failure — `undefined: New`.

- [ ] **Step 3: Implement the core**

`internal/core/core.go`:
```go
// Package core is the single-writer state machine. All hardware mutation goes
// through one goroutine; inputs submit Commands and observe StateChanges.
package core

import (
	"log"
	"strconv"

	"github.com/iot-root/garden-of-eden/internal/hw"
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

// Command is a request to mutate a device. Value is used by ActionOn (optional
// level) and ActionSetLevel.
type Command struct {
	Target Target
	Action Action
	Value  int
}

// StateChange is emitted after a command is applied. Topic is relative to the
// device base topic (e.g. "light/state"); the MQTT layer prefixes it.
type StateChange struct {
	Topic   string
	Payload string
}

type Core struct {
	dev    hw.Devices
	cmds   chan Command
	subs   []chan StateChange
	done   chan struct{}
	// last commanded levels, so ActionOn restores a sensible brightness/speed
	lightLevel int
	pumpLevel  int
}

func New(dev hw.Devices) *Core {
	return &Core{
		dev:        dev,
		cmds:       make(chan Command, 16),
		done:       make(chan struct{}),
		lightLevel: 50,
		pumpLevel:  100,
	}
}

// Subscribe returns a channel of state changes. Call before Run.
func (c *Core) Subscribe() <-chan StateChange {
	ch := make(chan StateChange, 32)
	c.subs = append(c.subs, ch)
	return ch
}

// Submit enqueues a command (non-blocking up to the buffer).
func (c *Core) Submit(cmd Command) { c.cmds <- cmd }

// Stop terminates the Run loop.
func (c *Core) Stop() { close(c.done) }

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
		c.emit("light/state", "ON")
		c.emit("light/brightness/state", strconv.Itoa(c.lightLevel))
	case ActionOff:
		if err := c.dev.Light.Off(); err != nil {
			log.Printf("light off: %v", err)
			return
		}
		c.emit("light/state", "OFF")
	case ActionSetLevel:
		c.lightLevel = cmd.Value
		if err := c.dev.Light.SetBrightness(cmd.Value); err != nil {
			log.Printf("light level: %v", err)
			return
		}
		c.emit("light/brightness/state", strconv.Itoa(cmd.Value))
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
		c.emit("pump/state", "ON")
		c.emit("pump/speed/state", strconv.Itoa(c.pumpLevel))
	case ActionOff:
		if err := c.dev.Pump.Off(); err != nil {
			log.Printf("pump off: %v", err)
			return
		}
		c.emit("pump/state", "OFF")
	case ActionSetLevel:
		c.pumpLevel = cmd.Value
		if err := c.dev.Pump.SetSpeed(cmd.Value); err != nil {
			log.Printf("pump level: %v", err)
			return
		}
		c.emit("pump/speed/state", strconv.Itoa(cmd.Value))
	}
}

func (c *Core) emit(topic, payload string) {
	for _, ch := range c.subs {
		select {
		case ch <- StateChange{Topic: topic, Payload: payload}:
		default: // never block the writer on a slow consumer
		}
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/core/ -v`
Expected: PASS.

---

### Task 5: MQTT discovery payload builders

**Depends on:** Task 2 (consumes `config.DeviceConfig`)

**Files:**
- Create: `internal/mqttsvc/discovery.go`
- Test: `internal/mqttsvc/discovery_test.go`

Pure functions that build the HA discovery topic + JSON for each entity. Topics
and `unique_id`s MUST match the current Python output; we additionally set
`availability_topic` (new in this rewrite).

- [ ] **Step 1: Write the failing test**

`internal/mqttsvc/discovery_test.go`:
```go
package mqttsvc

import (
	"encoding/json"
	"testing"

	"github.com/iot-root/garden-of-eden/internal/config"
)

func dev() config.DeviceConfig {
	return config.DeviceConfig{BaseTopic: "gardyn", Identifier: "gardyn-xx", Model: "gardyn 3.0", Version: "1.0.0"}
}

func TestLightDiscoveryTopicAndIDs(t *testing.T) {
	msgs := DiscoveryMessages(dev())
	m, ok := msgs["homeassistant/light/gardyn/gardyn-xx_light/config"]
	if !ok {
		t.Fatalf("light discovery topic missing; got %v", keys(msgs))
	}
	var p map[string]any
	if err := json.Unmarshal(m, &p); err != nil {
		t.Fatal(err)
	}
	if p["unique_id"] != "gardyn-xx_light" {
		t.Errorf("unique_id = %v", p["unique_id"])
	}
	if p["state_topic"] != "gardyn/light/state" {
		t.Errorf("state_topic = %v", p["state_topic"])
	}
	if p["command_topic"] != "gardyn/light/command" {
		t.Errorf("command_topic = %v", p["command_topic"])
	}
	if p["brightness_command_topic"] != "gardyn/light/brightness/set" {
		t.Errorf("brightness_command_topic = %v", p["brightness_command_topic"])
	}
	if p["availability_topic"] != "gardyn/availability" {
		t.Errorf("availability_topic = %v", p["availability_topic"])
	}
}

func TestPumpDiscoveryPresent(t *testing.T) {
	msgs := DiscoveryMessages(dev())
	m, ok := msgs["homeassistant/light/gardyn/gardyn-xx_pump/config"]
	if !ok {
		t.Fatalf("pump discovery topic missing; got %v", keys(msgs))
	}
	var p map[string]any
	if err := json.Unmarshal(m, &p); err != nil {
		t.Fatal(err)
	}
	if p["unique_id"] != "gardyn-xx_pump" {
		t.Errorf("unique_id = %v", p["unique_id"])
	}
	if p["command_topic"] != "gardyn/pump/command" {
		t.Errorf("command_topic = %v", p["command_topic"])
	}
}

func keys(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/mqttsvc/ -run Discovery -v`
Expected: build failure — `undefined: DiscoveryMessages`.

- [ ] **Step 3: Implement the builders**

`internal/mqttsvc/discovery.go`:
```go
package mqttsvc

import (
	"encoding/json"

	"github.com/iot-root/garden-of-eden/internal/config"
)

// AvailabilityTopic returns the LWT/availability topic for the device.
func AvailabilityTopic(d config.DeviceConfig) string { return d.BaseTopic + "/availability" }

func deviceInfo(d config.DeviceConfig) map[string]any {
	return map[string]any{
		"identifiers":  []string{d.Identifier},
		"name":         d.BaseTopic,
		"manufacturer": "gardyn-of-eden",
		"model":        d.Model,
		"sw_version":   d.Version,
	}
}

// DiscoveryMessages returns a map of retained discovery topic -> JSON payload.
// Plan 1 covers light + pump; later plans add sensors to this map.
func DiscoveryMessages(d config.DeviceConfig) map[string][]byte {
	base := d.BaseTopic
	avail := AvailabilityTopic(d)
	info := deviceInfo(d)
	out := map[string][]byte{}

	light := map[string]any{
		"name":                      "Light",
		"unique_id":                 d.Identifier + "_light",
		"platform":                  "mqtt",
		"state_topic":               base + "/light/state",
		"command_topic":             base + "/light/command",
		"brightness_state_topic":    base + "/light/brightness/state",
		"brightness_command_topic":  base + "/light/brightness/set",
		"brightness_scale":          100,
		"availability_topic":        avail,
		"device":                    info,
	}
	out["homeassistant/light/gardyn/"+d.Identifier+"_light/config"] = mustJSON(light)

	pump := map[string]any{
		"name":                     "Pump",
		"unique_id":                d.Identifier + "_pump",
		"platform":                 "mqtt",
		"device_class":             "fan",
		"state_topic":              base + "/pump/state",
		"command_topic":            base + "/pump/command",
		"brightness_state_topic":   base + "/pump/speed/state",
		"brightness_command_topic": base + "/pump/speed/set",
		"brightness_scale":         100,
		"icon":                     "mdi:water-pump",
		"availability_topic":       avail,
		"device":                   info,
	}
	out["homeassistant/light/gardyn/"+d.Identifier+"_pump/config"] = mustJSON(pump)

	return out
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err) // payloads are static maps; marshal cannot fail
	}
	return b
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/mqttsvc/ -run Discovery -v`
Expected: PASS.

---

### Task 6: MQTT client — connect, LWT, subscribe, dispatch, publish

**Depends on:** Task 4 (core `Command`/`StateChange`), Task 5 (discovery)

**Files:**
- Create: `internal/mqttsvc/mqttsvc.go`
- Test: `internal/mqttsvc/mqttsvc_test.go`

Wires paho to the core: subscribes to `<base>/#`, maps command topics to core
`Command`s, forwards core `StateChange`s to MQTT, sets a retained LWT on the
availability topic, and publishes discovery on connect. The test runs a real
in-process `mochi-mqtt` broker.

- [ ] **Step 1: Write the failing integration test**

`internal/mqttsvc/mqttsvc_test.go`:
```go
package mqttsvc

import (
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	mochi "github.com/mochi-mqtt/server/v2"
	"github.com/mochi-mqtt/server/v2/hooks/auth"
	"github.com/mochi-mqtt/server/v2/listeners"

	"github.com/iot-root/garden-of-eden/internal/config"
	"github.com/iot-root/garden-of-eden/internal/core"
	"github.com/iot-root/garden-of-eden/internal/hw/mock"
)

func startBroker(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()

	srv := mochi.New(nil)
	_ = srv.AddHook(new(auth.AllowHook), nil)
	tcp := listeners.NewTCP(listeners.Config{ID: "t", Address: addr})
	if err := srv.AddListener(tcp); err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve() }()
	t.Cleanup(func() { _ = srv.Close() })
	time.Sleep(100 * time.Millisecond)
	return addr
}

func subClient(t *testing.T, addr string) (mqtt.Client, *sync.Map) {
	t.Helper()
	host, port, _ := net.SplitHostPort(addr)
	opts := mqtt.NewClientOptions().AddBroker(fmt.Sprintf("tcp://%s:%s", host, port)).SetClientID("probe")
	got := &sync.Map{}
	cl := mqtt.NewClient(opts)
	if tok := cl.Connect(); tok.Wait() && tok.Error() != nil {
		t.Fatal(tok.Error())
	}
	cl.Subscribe("#", 0, func(_ mqtt.Client, m mqtt.Message) {
		got.Store(m.Topic(), string(m.Payload()))
	})
	return cl, got
}

func waitFor(got *sync.Map, topic, want string, d time.Duration) bool {
	deadline := time.After(d)
	for {
		if v, ok := got.Load(topic); ok && v.(string) == want {
			return true
		}
		select {
		case <-deadline:
			return false
		case <-time.After(20 * time.Millisecond):
		}
	}
}

func TestDiscoveryPublishedOnConnect(t *testing.T) {
	addr := startBroker(t)
	probe, got := subClient(t, addr)
	defer probe.Disconnect(100)

	cfg := config.Config{Device: config.DeviceConfig{BaseTopic: "gardyn", Identifier: "gardyn-xx"}}
	host, port, _ := net.SplitHostPort(addr)
	cfg.MQTT.Broker, cfg.MQTT.Port = host, atoiHelper(port)

	c := core.New(mock.New())
	go c.Run()
	defer c.Stop()

	svc, err := New(cfg, c)
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Stop()

	if !waitForExists(got, "homeassistant/light/gardyn/gardyn-xx_light/config", time.Second) {
		t.Error("light discovery not published")
	}
	if !waitFor(got, "gardyn/availability", "online", time.Second) {
		t.Error("availability online not published")
	}
}

func TestCommandTopicDrivesState(t *testing.T) {
	addr := startBroker(t)
	probe, got := subClient(t, addr)
	defer probe.Disconnect(100)

	cfg := config.Config{Device: config.DeviceConfig{BaseTopic: "gardyn", Identifier: "gardyn-xx"}}
	host, port, _ := net.SplitHostPort(addr)
	cfg.MQTT.Broker, cfg.MQTT.Port = host, atoiHelper(port)

	c := core.New(mock.New())
	go c.Run()
	defer c.Stop()
	svc, err := New(cfg, c)
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Stop()

	probe.Publish("gardyn/light/command", 0, false, "ON")
	if !waitFor(got, "gardyn/light/state", "ON", time.Second) {
		t.Error("light/state ON not published in response to command")
	}
}

func waitForExists(got *sync.Map, topic string, d time.Duration) bool {
	deadline := time.After(d)
	for {
		if _, ok := got.Load(topic); ok {
			return true
		}
		select {
		case <-deadline:
			return false
		case <-time.After(20 * time.Millisecond):
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/mqttsvc/ -run 'Discovery|Command' -v`
Expected: build failure — `undefined: New` (the service constructor) and `undefined: atoiHelper`.

- [ ] **Step 3: Implement the service**

`internal/mqttsvc/mqttsvc.go`:
```go
package mqttsvc

import (
	"fmt"
	"log"
	"strconv"
	"strings"

	mqtt "github.com/eclipse/paho.mqtt.golang"

	"github.com/iot-root/garden-of-eden/internal/config"
	"github.com/iot-root/garden-of-eden/internal/core"
)

type Service struct {
	cfg    config.Config
	core   *core.Core
	client mqtt.Client
	base   string
	done   chan struct{}
}

// atoiHelper is exported-for-tests style helper kept unexported but referenced
// by tests in the same package.
func atoiHelper(s string) int { n, _ := strconv.Atoi(s); return n }

// New connects to the broker, sets the LWT, subscribes, publishes discovery,
// and starts forwarding core state changes to MQTT.
func New(cfg config.Config, c *core.Core) (*Service, error) {
	s := &Service{cfg: cfg, core: c, base: cfg.Device.BaseTopic, done: make(chan struct{})}
	avail := AvailabilityTopic(cfg.Device)

	opts := mqtt.NewClientOptions().
		AddBroker(fmt.Sprintf("tcp://%s:%d", cfg.MQTT.Broker, cfg.MQTT.Port)).
		SetClientID("gardynd-" + cfg.Device.Identifier).
		SetWill(avail, "offline", 1, true).
		SetAutoReconnect(true).
		SetOnConnectHandler(s.onConnect)
	if cfg.MQTT.Username != "" {
		opts.SetUsername(cfg.MQTT.Username).SetPassword(cfg.MQTT.Password)
	}

	s.client = mqtt.NewClient(opts)
	if tok := s.client.Connect(); tok.Wait() && tok.Error() != nil {
		return nil, fmt.Errorf("mqtt connect: %w", tok.Error())
	}

	stateCh := c.Subscribe()
	go s.forwardState(stateCh)
	return s, nil
}

func (s *Service) onConnect(client mqtt.Client) {
	avail := AvailabilityTopic(s.cfg.Device)
	client.Publish(avail, 1, true, "online")
	for topic, payload := range DiscoveryMessages(s.cfg.Device) {
		client.Publish(topic, 0, true, payload)
	}
	client.Subscribe(s.base+"/#", 0, s.onMessage)
}

func (s *Service) forwardState(ch <-chan core.StateChange) {
	for {
		select {
		case <-s.done:
			return
		case sc := <-ch:
			s.client.Publish(s.base+"/"+sc.Topic, 0, false, sc.Payload)
		}
	}
}

func (s *Service) onMessage(_ mqtt.Client, m mqtt.Message) {
	suffix := strings.TrimPrefix(m.Topic(), s.base+"/")
	payload := strings.TrimSpace(string(m.Payload()))
	cmd, ok := mapCommand(suffix, payload)
	if !ok {
		return
	}
	s.core.Submit(cmd)
}

// mapCommand translates a command topic suffix + payload into a core.Command.
func mapCommand(suffix, payload string) (core.Command, bool) {
	up := strings.ToUpper(payload)
	switch suffix {
	case "light/command":
		if up == "ON" {
			return core.Command{Target: core.TargetLight, Action: core.ActionOn}, true
		}
		if up == "OFF" {
			return core.Command{Target: core.TargetLight, Action: core.ActionOff}, true
		}
	case "light/brightness/set":
		if n, err := strconv.Atoi(payload); err == nil {
			return core.Command{Target: core.TargetLight, Action: core.ActionSetLevel, Value: n}, true
		}
	case "pump/command":
		if up == "ON" {
			return core.Command{Target: core.TargetPump, Action: core.ActionOn}, true
		}
		if up == "OFF" {
			return core.Command{Target: core.TargetPump, Action: core.ActionOff}, true
		}
	case "pump/speed/set":
		if n, err := strconv.Atoi(payload); err == nil {
			return core.Command{Target: core.TargetPump, Action: core.ActionSetLevel, Value: n}, true
		}
	}
	return core.Command{}, false
}

// Stop publishes offline and disconnects.
func (s *Service) Stop() {
	close(s.done)
	if s.client != nil && s.client.IsConnected() {
		s.client.Publish(AvailabilityTopic(s.cfg.Device), 1, true, "offline")
		s.client.Disconnect(200)
	}
	log.Printf("mqtt service stopped")
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/mqttsvc/ -v`
Expected: PASS (both discovery and command tests).

---

### Task 7: REST API

**Depends on:** Task 4 (submits core `Command`s)

**Files:**
- Create: `internal/httpapi/httpapi.go`
- Test: `internal/httpapi/httpapi_test.go`

Preserves the Flask routes: `POST /light/on`, `POST /light/off`,
`POST /light/brightness {"value":N}`, the pump equivalents, plus `/healthz`.

- [ ] **Step 1: Write the failing test**

`internal/httpapi/httpapi_test.go`:
```go
package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/iot-root/garden-of-eden/internal/core"
	"github.com/iot-root/garden-of-eden/internal/hw/mock"
)

func TestLightOnRoute(t *testing.T) {
	devs := mock.New()
	c := core.New(devs)
	go c.Run()
	defer c.Stop()
	h := Handler(c)

	req := httptest.NewRequest(http.MethodPost, "/light/on", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !waitBrightness(devs, func(b int) bool { return b > 0 }) {
		t.Error("light not turned on")
	}
}

func TestLightBrightnessRoute(t *testing.T) {
	devs := mock.New()
	c := core.New(devs)
	go c.Run()
	defer c.Stop()
	h := Handler(c)

	req := httptest.NewRequest(http.MethodPost, "/light/brightness", strings.NewReader(`{"value":42}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if !waitBrightness(devs, func(b int) bool { return b == 42 }) {
		t.Error("brightness not set to 42")
	}
}

func TestHealthz(t *testing.T) {
	c := core.New(mock.New())
	go c.Run()
	defer c.Stop()
	rec := httptest.NewRecorder()
	Handler(c).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("healthz status = %d", rec.Code)
	}
}
```

Add the polling helper in the same file:
```go
import "time"

func waitBrightness(d interface{ Light interfaceLight }, pred func(int) bool) bool {
	return false // replaced below
}
```

> Implementation note for Step 3: instead of the placeholder above, the test
> uses the concrete mock. Replace the helper with the version below that reads
> the mock light directly.

- [ ] **Step 2: Replace the helper with a working version**

In `internal/httpapi/httpapi_test.go`, use:
```go
import (
	"time"

	"github.com/iot-root/garden-of-eden/internal/hw"
)

func waitBrightness(d hw.Devices, pred func(int) bool) bool {
	for i := 0; i < 50; i++ {
		if pred(d.Light.Brightness()) {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}
```
(Update the two call sites to pass `devs` of type `hw.Devices` — `mock.New()`
already returns that type.)

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/httpapi/ -v`
Expected: build failure — `undefined: Handler`.

- [ ] **Step 4: Implement the handlers**

`internal/httpapi/httpapi.go`:
```go
// Package httpapi exposes the REST control surface, submitting commands to the
// core. It mirrors the original Flask routes.
package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/iot-root/garden-of-eden/internal/core"
)

// Handler builds the REST mux bound to the given core.
func Handler(c *core.Core) http.Handler {
	mux := http.NewServeMux()

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

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
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

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/httpapi/ -v`
Expected: PASS.

---

### Task 8: Wire `main`, run full suite, single commit

**Depends on:** Tasks 2, 3, 4, 5, 6, 7

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
	"github.com/iot-root/garden-of-eden/internal/mqttsvc"
)

func main() {
	configPath := flag.String("config", "", "path to YAML config file")
	hwMode := flag.String("hw", "real", "hardware backend: real|mock")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("config: %v", err)
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

	c := core.New(devs)
	go c.Run()
	defer c.Stop()

	svc, err := mqttsvc.New(cfg, c)
	if err != nil {
		log.Fatalf("mqtt: %v", err)
	}
	defer svc.Stop()

	addr := fmt.Sprintf(":%d", cfg.HTTP.Port)
	server := &http.Server{Addr: addr, Handler: httpapi.Handler(c)}
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

- [ ] **Step 2: Tidy modules**

Run: `make tidy`
Expected: `go.mod`/`go.sum` updated, no errors.

- [ ] **Step 3: Build for host and for the Pi**

Run: `make build && make build-pi`
Expected: `bin/gardynd` and `bin/gardynd-armv6` produced.

- [ ] **Step 4: Run the full test suite**

Run: `go test ./...`
Expected: all packages PASS.

- [ ] **Step 5: Manual smoke (optional but recommended)**

Run: `./bin/gardynd --hw=mock` against a local mosquitto, then in another shell:
`curl -X POST localhost:5000/light/on` and confirm `gardyn/light/state ON`
appears (e.g. `mosquitto_sub -t 'gardyn/#'`).

- [ ] **Step 6: Commit (single commit for the whole plan)**

Run:
```
git add go.mod go.sum Makefile cmd/ internal/
git commit -m "feat: gardynd skeleton — light+pump over REST+MQTT with HA discovery"
```
Expected: one commit containing all Plan 1 changes.

---

## Self-Review

**Spec coverage (Plan 1 portion):** single binary scaffold ✓; mock backend /
`--hw=mock` laptop dev loop ✓; single-writer core ✓; REST parity for
light+pump ✓; MQTT discovery with preserved topics/`unique_id`s ✓; availability
LWT ✓; embedded-broker integration test ✓; ARMv6 cross-compile target ✓.
Deferred by design to later plans: real drivers (Plan 2); scheduler,
water-low interlock, over-temp, pump failsafe, sensors, camera, button (Plan
3); systemd/CI/README/Python removal (Plan 4). No Plan-1 requirement is
unaddressed.

**Placeholder scan:** Task 7 Step 1 intentionally shows a placeholder helper
that Step 2 replaces with the real version — flagged inline so the executor
doesn't ship it. No other placeholders.

**Type consistency:** `hw.Devices{Light, Pump}`, `core.Command{Target, Action,
Value}`, `core.StateChange{Topic, Payload}`, `core.New(hw.Devices)`,
`mqttsvc.New(config.Config, *core.Core)`, `httpapi.Handler(*core.Core)`,
`DiscoveryMessages(config.DeviceConfig)`, `AvailabilityTopic(config.DeviceConfig)`
are used consistently across tasks. State topics emitted by core
(`light/state`, `light/brightness/state`, `pump/state`, `pump/speed/state`) are
prefixed with `<base>/` by `forwardState`, matching the discovery topics.

**Dependency audit:** Task 1 (none) creates the module/stub. Tasks 2 and 3 both
depend only on Task 1 and touch disjoint files (`internal/config` vs
`internal/hw`) → safe to parallelize. Task 5 depends on Task 2 (uses
`config.DeviceConfig`). Task 4 depends on Task 3. Task 6 depends on 4+5 (shares
the `internal/mqttsvc` package with Task 5 — serialized correctly). Task 7
depends on Task 4. Task 8 modifies `cmd/gardynd/main.go` (created in Task 1) and
imports every package → depends on all. No `Depends on: none` task shares files
with another. Audit clean.
