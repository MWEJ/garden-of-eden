# gardynd Plan 2 — Real Hardware Drivers Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement real Raspberry Pi drivers behind the Plan 1 `hw` interfaces (light PWM, pump PWM, HC-SR04 distance, I2C env/PCB-temp/power sensors, V4L2 cameras, GPIO button), wire `--hw=real`, and port the Flask sensor GET routes so sensors are observable.

**Architecture:** Each driver implements an `hw` interface and is constructed by a single `real.New(cfg)` factory. Pure-logic pieces (median filter, AM2320 CRC, INA219 math, PCT2075 decode, button FSM) are unit-tested off-Pi; raw GPIO/I2C/V4L2 paths get on-device verification steps. I2C bus access is serialized behind one mutex.

**Tech Stack:** `github.com/warthog618/go-gpiocdev` (cdev GPIO), Linux `sysfs` PWM, `periph.io/x/conn/v3/i2c` + `periph.io/x/host/v3` (I2C), `github.com/vladimirvivien/go4vl` (V4L2).

**Spec:** `docs/superpowers/specs/2026-06-12-gardynd-go-service-design.md`
**Depends on:** Plan 1 (interfaces, `hw.Devices`, mock, core, httpapi).

---

## File Structure (this plan)

```
internal/hw/hw.go                 MODIFY: add sensor interfaces + extend Devices
internal/hw/mock/mock.go          MODIFY: implement new interfaces
internal/hw/real/light_pwm.go     sysfs hardware PWM (GPIO18)
internal/hw/real/pump_pwm.go      soft-PWM (GPIO24)
internal/hw/real/distance.go      HC-SR04 (+ median, unit-tested)
internal/hw/real/i2cbus.go        mutex-guarded I2C bus handle
internal/hw/real/env.go           AM2320 / AHT20 (+ CRC, unit-tested)
internal/hw/real/pcbtemp.go       PCT2075 (+ decode, unit-tested)
internal/hw/real/power.go         INA219 (+ math, unit-tested)
internal/hw/real/camera.go        V4L2 capture
internal/hw/real/button.go        GPIO13 FSM (+ unit-tested)
internal/hw/real/real.go          New(cfg) hw.Devices factory
internal/httpapi/httpapi.go       MODIFY: add sensor GET routes
cmd/gardynd/main.go               MODIFY: wire --hw=real
```

---

### Task 1: Extend interfaces, Devices, and mock

**Depends on:** none (within this plan)

**Files:**
- Modify: `internal/hw/hw.go`
- Modify: `internal/hw/mock/mock.go`
- Test: `internal/hw/mock/mock_test.go` (append)

- [ ] **Step 1: Add the sensor interfaces and extend Devices**

Append to `internal/hw/hw.go`:
```go
// DistanceSensor measures water-tank distance in centimeters.
type DistanceSensor interface {
	MeasureCM() (float64, error)
}

// EnvSensor reads ambient temperature (°C) and relative humidity (%).
type EnvSensor interface {
	Read() (tempC float64, humidityPct float64, err error)
}

// PCBTempSensor reads board temperature and the over-temp alert state.
type PCBTempSensor interface {
	Temperature() (float64, error)
	OverTemp() (bool, error)
}

// PowerReading is an INA219 sample.
type PowerReading struct {
	BusVoltage   float64
	ShuntVoltage float64
	Current      float64
	Power        float64
}

// PowerSensor reads pump power telemetry.
type PowerSensor interface {
	Read() (PowerReading, error)
}

// Camera captures a JPEG frame.
type Camera interface {
	Capture() ([]byte, error)
}

// ButtonEvent is a debounced button gesture.
type ButtonEvent int

const (
	SinglePress ButtonEvent = iota
	DoublePress
)

// Button delivers debounced press gestures.
type Button interface {
	Events() <-chan ButtonEvent
}
```

Change the `Devices` struct in `internal/hw/hw.go` to:
```go
// Devices bundles the hardware the service controls. Sensor fields may be nil
// when a sensor failed to initialize (mirrors the Python "sensor == None" guard).
type Devices struct {
	Light       Light
	Pump        Pump
	Distance    DistanceSensor
	Env         EnvSensor
	PCBTemp     PCBTempSensor
	Power       PowerSensor
	UpperCamera Camera
	LowerCamera Camera
	Button      Button
}
```

- [ ] **Step 2: Write the failing mock test (append)**

Append to `internal/hw/mock/mock_test.go`:
```go
func TestMockSensors(t *testing.T) {
	d := New()
	if c, err := d.Distance.MeasureCM(); err != nil || c <= 0 {
		t.Errorf("distance = %v, %v", c, err)
	}
	temp, hum, err := d.Env.Read()
	if err != nil || temp == 0 || hum == 0 {
		t.Errorf("env = %v/%v err %v", temp, hum, err)
	}
	if _, err := d.PCBTemp.Temperature(); err != nil {
		t.Errorf("pcb temp err %v", err)
	}
	if r, err := d.Power.Read(); err != nil || r.BusVoltage == 0 {
		t.Errorf("power = %+v err %v", r, err)
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/hw/mock/ -run TestMockSensors -v`
Expected: build failure — `d.Distance` undefined.

- [ ] **Step 4: Implement the mock sensors**

Append to `internal/hw/mock/mock.go`:
```go
type Distance struct{ CM float64 }

func (d *Distance) MeasureCM() (float64, error) {
	if d.CM == 0 {
		return 7.5, nil
	}
	return d.CM, nil
}

type Env struct{ T, H float64 }

func (e *Env) Read() (float64, float64, error) {
	t, h := e.T, e.H
	if t == 0 {
		t = 22.5
	}
	if h == 0 {
		h = 55.0
	}
	return t, h, nil
}

type PCBTemp struct {
	T    float64
	Over bool
}

func (p *PCBTemp) Temperature() (float64, error) {
	if p.T == 0 {
		return 30.0, nil
	}
	return p.T, nil
}
func (p *PCBTemp) OverTemp() (bool, error) { return p.Over, nil }

type Power struct{}

func (Power) Read() (hw.PowerReading, error) {
	return hw.PowerReading{BusVoltage: 12.0, ShuntVoltage: 0.01, Current: 0.5, Power: 6.0}, nil
}

type Camera struct{}

// 1x1 JPEG-ish stub; tests only check non-empty.
func (Camera) Capture() ([]byte, error) { return []byte{0xFF, 0xD8, 0xFF, 0xD9}, nil }

type Button struct{ ch chan hw.ButtonEvent }

func (b *Button) Events() <-chan hw.ButtonEvent {
	if b.ch == nil {
		b.ch = make(chan hw.ButtonEvent)
	}
	return b.ch
}
```

Update `mock.New()` to populate the new fields:
```go
func New() hw.Devices {
	return hw.Devices{
		Light:       &Light{},
		Pump:        &Pump{},
		Distance:    &Distance{},
		Env:         &Env{},
		PCBTemp:     &PCBTemp{},
		Power:       Power{},
		UpperCamera: Camera{},
		LowerCamera: Camera{},
		Button:      &Button{},
	}
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/hw/mock/ -v`
Expected: PASS (existing + new tests).

---

### Task 2: Light driver — sysfs hardware PWM (GPIO18)

**Depends on:** Task 1

**Files:**
- Create: `internal/hw/real/light_pwm.go`

GPIO18 is a hardware-PWM pin (PWM0). We drive it via the kernel `pwm` sysfs
interface at 8 kHz. Requires `dtoverlay=pwm` in `/boot/config.txt` (documented
in Plan 4). Brightness 0..100 maps to duty-cycle nanoseconds.

- [ ] **Step 1: Implement the driver**

`internal/hw/real/light_pwm.go`:
```go
package real

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

const pwmChip = "/sys/class/pwm/pwmchip0"

// pwmLine drives one hardware-PWM channel via sysfs. periodNS is fixed; duty
// is set per brightness/speed.
type pwmLine struct {
	mu       sync.Mutex
	dir      string // e.g. /sys/class/pwm/pwmchip0/pwm0
	periodNS int
	pct      int
}

func newPWMLine(channel, freqHz int) (*pwmLine, error) {
	if _, err := os.Stat(filepath.Join(pwmChip, fmt.Sprintf("pwm%d", channel))); os.IsNotExist(err) {
		if err := os.WriteFile(filepath.Join(pwmChip, "export"), []byte(strconv.Itoa(channel)), 0o644); err != nil {
			return nil, fmt.Errorf("export pwm%d: %w (is dtoverlay=pwm enabled?)", channel, err)
		}
		time.Sleep(50 * time.Millisecond) // udev needs a moment to create the dir
	}
	dir := filepath.Join(pwmChip, fmt.Sprintf("pwm%d", channel))
	periodNS := int(time.Second) / freqHz
	l := &pwmLine{dir: dir, periodNS: periodNS}
	if err := l.write("period", strconv.Itoa(periodNS)); err != nil {
		return nil, err
	}
	if err := l.write("duty_cycle", "0"); err != nil {
		return nil, err
	}
	if err := l.write("enable", "1"); err != nil {
		return nil, err
	}
	return l, nil
}

func (l *pwmLine) write(attr, val string) error {
	return os.WriteFile(filepath.Join(l.dir, attr), []byte(val), 0o644)
}

func (l *pwmLine) setPercent(pct int) error {
	if pct < 0 || pct > 100 {
		return fmt.Errorf("duty %d out of range 0..100", pct)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	duty := l.periodNS * pct / 100
	if err := l.write("duty_cycle", strconv.Itoa(duty)); err != nil {
		return err
	}
	l.pct = pct
	return nil
}

func (l *pwmLine) percent() int { l.mu.Lock(); defer l.mu.Unlock(); return l.pct }

// LightPWM implements hw.Light on PWM channel 0 (GPIO18) at 8 kHz.
type LightPWM struct{ line *pwmLine }

func NewLightPWM() (*LightPWM, error) {
	l, err := newPWMLine(0, 8000)
	if err != nil {
		return nil, err
	}
	return &LightPWM{line: l}, nil
}

func (l *LightPWM) SetBrightness(pct int) error { return l.line.setPercent(pct) }
func (l *LightPWM) Brightness() int             { return l.line.percent() }
func (l *LightPWM) Off() error                  { return l.line.setPercent(0) }
```

- [ ] **Step 2: Verify it compiles**

Run: `go build ./internal/hw/real/`
Expected: builds (no hardware needed to compile).

- [ ] **Step 3: On-device verification (Pi only)**

After Task 11 wires `--hw=real`: with `dtoverlay=pwm` enabled, run the service
and `curl -X POST localhost:5000/light/brightness -d '{"value":70}'`; confirm
the light dims to ~70%. (No automated test — hardware in the loop.)

---

### Task 3: Pump driver — soft-PWM (GPIO24)

**Depends on:** Task 1

**Files:**
- Create: `internal/hw/real/pump_pwm.go`

GPIO24 is not a hardware-PWM pin, so we run a software-PWM goroutine at 50 Hz
using `gpiocdev`. A 50 Hz period is 20 ms; the goroutine holds the line high for
`pct%` of each period.

- [ ] **Step 1: Implement the driver**

`internal/hw/real/pump_pwm.go`:
```go
package real

import (
	"fmt"
	"sync"
	"time"

	"github.com/warthog618/go-gpiocdev"
)

// PumpPWM implements hw.Pump via a software-PWM goroutine at the given freq.
type PumpPWM struct {
	line   *gpiocdev.Line
	period time.Duration
	mu     sync.Mutex
	pct    int
	done   chan struct{}
}

func NewPumpPWM(chip string, gpio, freqHz int) (*PumpPWM, error) {
	line, err := gpiocdev.RequestLine(chip, gpio, gpiocdev.AsOutput(0))
	if err != nil {
		return nil, fmt.Errorf("request gpio%d: %w", gpio, err)
	}
	p := &PumpPWM{
		line:   line,
		period: time.Second / time.Duration(freqHz),
		done:   make(chan struct{}),
	}
	go p.loop()
	return p, nil
}

func (p *PumpPWM) loop() {
	for {
		select {
		case <-p.done:
			return
		default:
		}
		p.mu.Lock()
		pct := p.pct
		p.mu.Unlock()

		switch {
		case pct <= 0:
			_ = p.line.SetValue(0)
			time.Sleep(p.period)
		case pct >= 100:
			_ = p.line.SetValue(1)
			time.Sleep(p.period)
		default:
			high := p.period * time.Duration(pct) / 100
			_ = p.line.SetValue(1)
			time.Sleep(high)
			_ = p.line.SetValue(0)
			time.Sleep(p.period - high)
		}
	}
}

func (p *PumpPWM) SetSpeed(pct int) error {
	if pct < 0 || pct > 100 {
		return fmt.Errorf("speed %d out of range 0..100", pct)
	}
	p.mu.Lock()
	p.pct = pct
	p.mu.Unlock()
	return nil
}

func (p *PumpPWM) Speed() int { p.mu.Lock(); defer p.mu.Unlock(); return p.pct }
func (p *PumpPWM) Off() error { return p.SetSpeed(0) }

func (p *PumpPWM) Close() error {
	close(p.done)
	return p.line.Close()
}
```

- [ ] **Step 2: Add the dependency and verify build**

Run: `go get github.com/warthog618/go-gpiocdev@latest && go build ./internal/hw/real/`
Expected: builds.

- [ ] **Step 3: On-device verification (Pi only)**

`curl -X POST localhost:5000/pump/on` → pump runs; `/pump/off` → stops. Confirm
no audible buzzing at idle (line held low when pct=0).

---

### Task 4: HC-SR04 distance sensor (+ median, unit-tested)

**Depends on:** Task 1

**Files:**
- Create: `internal/hw/real/distance.go`
- Test: `internal/hw/real/distance_test.go`

Trigger on GPIO26, echo on GPIO19. We pulse the trigger, measure the echo pulse
width using `gpiocdev` edge events (kernel timestamps), convert to cm, and
return the median of several readings — the median math is unit-tested.

- [ ] **Step 1: Write the failing test for the median**

`internal/hw/real/distance_test.go`:
```go
package real

import (
	"math"
	"testing"
)

func TestMedian(t *testing.T) {
	cases := []struct {
		in   []float64
		want float64
	}{
		{[]float64{3, 1, 2}, 2},
		{[]float64{4, 1, 3, 2}, 2.5},
		{[]float64{5}, 5},
	}
	for _, c := range cases {
		if got := median(c.in); math.Abs(got-c.want) > 1e-9 {
			t.Errorf("median(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestMedianEmptyPanicsGuard(t *testing.T) {
	if _, err := medianOrErr(nil); err == nil {
		t.Error("expected error for empty input")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/hw/real/ -run Median -v`
Expected: build failure — `undefined: median`.

- [ ] **Step 3: Implement the driver**

`internal/hw/real/distance.go`:
```go
package real

import (
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/warthog618/go-gpiocdev"
)

const speedOfSoundCmPerS = 34300.0 // ~343 m/s

// HCSR04 implements hw.DistanceSensor.
type HCSR04 struct {
	mu      sync.Mutex
	chip    string
	trigger int
	echo    int
}

func NewHCSR04(chip string, trigger, echo int) *HCSR04 {
	return &HCSR04{chip: chip, trigger: trigger, echo: echo}
}

func (h *HCSR04) MeasureCM() (float64, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	var samples []float64
	for i := 0; i < 10; i++ {
		if cm, err := h.measureOnce(); err == nil {
			samples = append(samples, cm)
		}
		time.Sleep(20 * time.Millisecond) // avoid echo overlap
	}
	return medianOrErr(samples)
}

func (h *HCSR04) measureOnce() (float64, error) {
	trig, err := gpiocdev.RequestLine(h.chip, h.trigger, gpiocdev.AsOutput(0))
	if err != nil {
		return 0, err
	}
	defer trig.Close()

	rise := make(chan time.Time, 1)
	fall := make(chan time.Time, 1)
	echo, err := gpiocdev.RequestLine(h.chip, h.echo,
		gpiocdev.WithBothEdges,
		gpiocdev.WithEventHandler(func(ev gpiocdev.LineEvent) {
			ts := time.Now()
			if ev.Type == gpiocdev.LineEventRisingEdge {
				select {
				case rise <- ts:
				default:
				}
			} else {
				select {
				case fall <- ts:
				default:
				}
			}
		}))
	if err != nil {
		return 0, err
	}
	defer echo.Close()

	// 10µs trigger pulse.
	_ = trig.SetValue(1)
	time.Sleep(10 * time.Microsecond)
	_ = trig.SetValue(0)

	var t0 time.Time
	select {
	case t0 = <-rise:
	case <-time.After(60 * time.Millisecond):
		return 0, fmt.Errorf("echo rise timeout")
	}
	select {
	case t1 := <-fall:
		width := t1.Sub(t0).Seconds()
		cm := width * speedOfSoundCmPerS / 2
		if cm <= 0 || cm > 400 {
			return 0, fmt.Errorf("implausible distance %.1fcm", cm)
		}
		return round2(cm), nil
	case <-time.After(60 * time.Millisecond):
		return 0, fmt.Errorf("echo fall timeout")
	}
}

func median(data []float64) float64 {
	s := append([]float64(nil), data...)
	sort.Float64s(s)
	n := len(s)
	if n%2 == 1 {
		return s[n/2]
	}
	return (s[n/2-1] + s[n/2]) / 2
}

func medianOrErr(data []float64) (float64, error) {
	if len(data) == 0 {
		return 0, fmt.Errorf("no successful measurements")
	}
	return round2(median(data)), nil
}

func round2(f float64) float64 { return float64(int(f*100+0.5)) / 100 }
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/hw/real/ -run Median -v`
Expected: PASS.

- [ ] **Step 5: On-device verification (Pi only)**

`curl localhost:5000/distance` returns a plausible tank distance in cm.

---

### Task 5: I2C bus + AM2320/AHT20 env sensor (+ CRC, unit-tested)

**Depends on:** Task 1

**Files:**
- Create: `internal/hw/real/i2cbus.go`
- Create: `internal/hw/real/env.go`
- Test: `internal/hw/real/env_test.go`

All I2C devices share `/dev/i2c-1`; a single mutex serializes transactions. The
AM2320 read returns CRC-16 (Modbus) checked data — the CRC is unit-tested.

- [ ] **Step 1: Implement the bus wrapper**

`internal/hw/real/i2cbus.go`:
```go
package real

import (
	"sync"

	"periph.io/x/conn/v3/i2c"
	"periph.io/x/conn/v3/i2c/i2creg"
	"periph.io/x/host/v3"
)

// Bus is a mutex-guarded shared I2C bus.
type Bus struct {
	mu  sync.Mutex
	bus i2c.BusCloser
}

func OpenBus() (*Bus, error) {
	if _, err := host.Init(); err != nil {
		return nil, err
	}
	b, err := i2creg.Open("/dev/i2c-1")
	if err != nil {
		return nil, err
	}
	return &Bus{bus: b}, nil
}

// Tx performs a serialized transaction against addr.
func (b *Bus) Tx(addr uint16, w, r []byte) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.bus.Tx(addr, w, r)
}

func (b *Bus) Close() error { return b.bus.Close() }
```

- [ ] **Step 2: Write the failing CRC test**

`internal/hw/real/env_test.go`:
```go
package real

import "testing"

func TestAM2320CRC(t *testing.T) {
	// Frame: function 0x03, length 0x04, 4 data bytes. CRC computed over the
	// first 6 bytes (little-endian result appended).
	frame := []byte{0x03, 0x04, 0x01, 0x90, 0x01, 0xF4}
	lo, hi := am2320CRC(frame)
	if got := crc16(frame); (byte(got), byte(got>>8)) != (lo, hi) {
		t.Errorf("crc mismatch: split=%02x%02x full=%04x", hi, lo, got)
	}
	// Round-trip: appending the CRC then validating must pass.
	full := append(append([]byte{}, frame...), lo, hi)
	if !am2320Valid(full) {
		t.Error("am2320Valid rejected a self-consistent frame")
	}
	full[2] ^= 0xFF // corrupt a data byte
	if am2320Valid(full) {
		t.Error("am2320Valid accepted a corrupted frame")
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/hw/real/ -run CRC -v`
Expected: build failure — `undefined: am2320CRC`.

- [ ] **Step 4: Implement the env sensor**

`internal/hw/real/env.go`:
```go
package real

import (
	"fmt"
	"time"

	"github.com/iot-root/garden-of-eden/internal/hw"
)

// crc16 is the Modbus CRC-16 used by the AM2320.
func crc16(data []byte) uint16 {
	crc := uint16(0xFFFF)
	for _, b := range data {
		crc ^= uint16(b)
		for i := 0; i < 8; i++ {
			if crc&1 == 1 {
				crc = (crc >> 1) ^ 0xA001
			} else {
				crc >>= 1
			}
		}
	}
	return crc
}

// am2320CRC returns the low and high CRC bytes for the given frame.
func am2320CRC(frame []byte) (lo, hi byte) {
	c := crc16(frame)
	return byte(c & 0xFF), byte(c >> 8)
}

// am2320Valid checks a full frame (data + trailing little-endian CRC).
func am2320Valid(full []byte) bool {
	if len(full) < 3 {
		return false
	}
	body := full[:len(full)-2]
	lo, hi := full[len(full)-2], full[len(full)-1]
	wantLo, wantHi := am2320CRC(body)
	return lo == wantLo && hi == wantHi
}

// EnvAM2320 implements hw.EnvSensor for the AM2320 at 0x5C.
type EnvAM2320 struct {
	bus  *Bus
	addr uint16
}

func NewEnvAM2320(bus *Bus) *EnvAM2320 { return &EnvAM2320{bus: bus, addr: 0x5C} }

func (e *EnvAM2320) Read() (float64, float64, error) {
	// Wake (AM2320 sleeps; first tx wakes it and is NACKed).
	_ = e.bus.Tx(e.addr, []byte{0x00}, nil)
	time.Sleep(2 * time.Millisecond)
	// Read 4 registers starting at 0x00 (humidity hi/lo, temp hi/lo).
	if err := e.bus.Tx(e.addr, []byte{0x03, 0x00, 0x04}, nil); err != nil {
		return 0, 0, err
	}
	time.Sleep(2 * time.Millisecond)
	buf := make([]byte, 8) // fn, len, 4 data, crc lo, crc hi
	if err := e.bus.Tx(e.addr, nil, buf); err != nil {
		return 0, 0, err
	}
	if !am2320Valid(buf) {
		return 0, 0, fmt.Errorf("am2320 CRC mismatch")
	}
	hum := float64(uint16(buf[2])<<8|uint16(buf[3])) / 10.0
	temp := float64(uint16(buf[4])<<8|uint16(buf[5])) / 10.0
	return temp, hum, nil
}

// EnvAHT20 implements hw.EnvSensor for the AHT20/DHT20 at 0x38.
type EnvAHT20 struct {
	bus  *Bus
	addr uint16
}

func NewEnvAHT20(bus *Bus) *EnvAHT20 { return &EnvAHT20{bus: bus, addr: 0x38} }

func (e *EnvAHT20) Read() (float64, float64, error) {
	if err := e.bus.Tx(e.addr, []byte{0xAC, 0x33, 0x00}, nil); err != nil {
		return 0, 0, err
	}
	time.Sleep(80 * time.Millisecond)
	buf := make([]byte, 7)
	if err := e.bus.Tx(e.addr, nil, buf); err != nil {
		return 0, 0, err
	}
	if buf[0]&0x80 != 0 {
		return 0, 0, fmt.Errorf("aht20 still busy")
	}
	rawHum := (uint32(buf[1]) << 12) | (uint32(buf[2]) << 4) | (uint32(buf[3]) >> 4)
	rawTemp := ((uint32(buf[3]) & 0x0F) << 16) | (uint32(buf[4]) << 8) | uint32(buf[5])
	hum := float64(rawHum) * 100.0 / 1048576.0
	temp := float64(rawTemp)*200.0/1048576.0 - 50.0
	return temp, hum, nil
}

var _ hw.EnvSensor = (*EnvAM2320)(nil)
var _ hw.EnvSensor = (*EnvAHT20)(nil)
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/hw/real/ -run CRC -v`
Expected: PASS.

---

### Task 6: PCT2075 PCB temperature + over-temp (+ decode, unit-tested)

**Depends on:** Task 1, Task 5 (uses `Bus`)

**Files:**
- Create: `internal/hw/real/pcbtemp.go`
- Test: `internal/hw/real/pcbtemp_test.go`

PCT2075 at 0x48. Temperature register is a 16-bit big-endian value, left-aligned
11-bit two's-complement, LSB = 0.125 °C. The decode is unit-tested. The
over-temp alert is read from GPIO25 (configured active-high by the device's OS
pin).

- [ ] **Step 1: Write the failing decode test**

`internal/hw/real/pcbtemp_test.go`:
```go
package real

import (
	"math"
	"testing"
)

func TestPCT2075Decode(t *testing.T) {
	cases := []struct {
		hi, lo byte
		want   float64
	}{
		{0x19, 0x00, 25.0},   // 0b0011001 -> 25 * ... 0x1900>>5 = 0xC8 = 200 *0.125
		{0x00, 0x00, 0.0},
		{0xFF, 0x80, -0.5},   // negative
	}
	for _, c := range cases {
		if got := decodePCT2075(c.hi, c.lo); math.Abs(got-c.want) > 0.01 {
			t.Errorf("decode(%02x%02x) = %v, want %v", c.hi, c.lo, got, c.want)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/hw/real/ -run PCT2075 -v`
Expected: build failure — `undefined: decodePCT2075`.

- [ ] **Step 3: Implement the driver**

`internal/hw/real/pcbtemp.go`:
```go
package real

import (
	"github.com/iot-root/garden-of-eden/internal/hw"
	"github.com/warthog618/go-gpiocdev"
)

// decodePCT2075 converts the 2-byte temperature register to °C.
func decodePCT2075(hi, lo byte) float64 {
	raw := int16(uint16(hi)<<8|uint16(lo)) >> 5 // 11-bit, right-aligned
	return float64(raw) * 0.125
}

// PCT2075 implements hw.PCBTempSensor. alertLine is optional (may be nil).
type PCT2075 struct {
	bus       *Bus
	addr      uint16
	alertLine *gpiocdev.Line
}

func NewPCT2075(bus *Bus, chip string, alertGPIO int) (*PCT2075, error) {
	p := &PCT2075{bus: bus, addr: 0x48}
	if alertGPIO >= 0 {
		line, err := gpiocdev.RequestLine(chip, alertGPIO, gpiocdev.AsInput)
		if err != nil {
			return nil, err
		}
		p.alertLine = line
	}
	return p, nil
}

func (p *PCT2075) Temperature() (float64, error) {
	buf := make([]byte, 2)
	if err := p.bus.Tx(p.addr, []byte{0x00}, buf); err != nil { // 0x00 = temp register
		return 0, err
	}
	return decodePCT2075(buf[0], buf[1]), nil
}

func (p *PCT2075) OverTemp() (bool, error) {
	if p.alertLine == nil {
		return false, nil
	}
	v, err := p.alertLine.Value()
	if err != nil {
		return false, err
	}
	return v == 1, nil // active-high alert
}

var _ hw.PCBTempSensor = (*PCT2075)(nil)
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/hw/real/ -run PCT2075 -v`
Expected: PASS.

---

### Task 7: INA219 pump power (+ math, unit-tested)

**Depends on:** Task 1, Task 5 (uses `Bus`)

**Files:**
- Create: `internal/hw/real/power.go`
- Test: `internal/hw/real/power_test.go`

INA219 at 0x40, shunt 0.08 Ω. Bus voltage register is bits [15:3] × 4 mV; shunt
voltage register LSB = 10 µV; current = shunt voltage / shunt resistance. The
conversions are unit-tested; configuration/register reads are on-device.

- [ ] **Step 1: Write the failing math test**

`internal/hw/real/power_test.go`:
```go
package real

import (
	"math"
	"testing"
)

func TestINA219BusVoltage(t *testing.T) {
	// 0x1F40 = 8000 raw; >>3 = 1000; ×0.004 V = 4.000 V
	if got := busVoltageV(0x1F40); math.Abs(got-4.0) > 1e-9 {
		t.Errorf("busVoltageV = %v, want 4.0", got)
	}
}

func TestINA219ShuntAndCurrent(t *testing.T) {
	// shunt raw 1000 × 10µV = 0.01 V; current = 0.01 / 0.08 = 0.125 A
	v := shuntVoltageV(1000)
	if math.Abs(v-0.01) > 1e-9 {
		t.Fatalf("shuntVoltageV = %v", v)
	}
	if got := currentA(v, 0.08); math.Abs(got-0.125) > 1e-9 {
		t.Errorf("currentA = %v, want 0.125", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/hw/real/ -run INA219 -v`
Expected: build failure — `undefined: busVoltageV`.

- [ ] **Step 3: Implement the driver**

`internal/hw/real/power.go`:
```go
package real

import (
	"github.com/iot-root/garden-of-eden/internal/hw"
)

func busVoltageV(raw uint16) float64   { return float64(raw>>3) * 0.004 }
func shuntVoltageV(raw int16) float64  { return float64(raw) * 0.00001 } // 10µV LSB
func currentA(shuntV, shuntOhms float64) float64 { return shuntV / shuntOhms }

// INA219 implements hw.PowerSensor.
type INA219 struct {
	bus       *Bus
	addr      uint16
	shuntOhms float64
}

func NewINA219(bus *Bus) *INA219 { return &INA219{bus: bus, addr: 0x40, shuntOhms: 0.08} }

func (s *INA219) readReg(reg byte) (uint16, error) {
	buf := make([]byte, 2)
	if err := s.bus.Tx(s.addr, []byte{reg}, buf); err != nil {
		return 0, err
	}
	return uint16(buf[0])<<8 | uint16(buf[1]), nil
}

func (s *INA219) Read() (hw.PowerReading, error) {
	busRaw, err := s.readReg(0x02) // bus voltage register
	if err != nil {
		return hw.PowerReading{}, err
	}
	shuntRaw, err := s.readReg(0x01) // shunt voltage register
	if err != nil {
		return hw.PowerReading{}, err
	}
	bv := busVoltageV(busRaw)
	sv := shuntVoltageV(int16(shuntRaw))
	cur := currentA(sv, s.shuntOhms)
	return hw.PowerReading{
		BusVoltage:   round2(bv),
		ShuntVoltage: sv,
		Current:      round2(cur),
		Power:        round2(bv * cur),
	}, nil
}

var _ hw.PowerSensor = (*INA219)(nil)
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/hw/real/ -run INA219 -v`
Expected: PASS.

---

### Task 8: V4L2 camera

**Depends on:** Task 1

**Files:**
- Create: `internal/hw/real/camera.go`

Native MJPEG capture via go4vl, replacing the `fswebcam` subprocess. Skips a few
warm-up frames (the old code used `-S 2 -F 2`) and returns one JPEG.

- [ ] **Step 1: Implement the driver**

`internal/hw/real/camera.go`:
```go
package real

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/vladimirvivien/go4vl/device"
	"github.com/vladimirvivien/go4vl/v4l2"
)

// V4L2Camera implements hw.Camera. resolution is "WxH" e.g. "640x480".
type V4L2Camera struct {
	devPath string
	w, h    uint32
}

func NewV4L2Camera(devPath, resolution string) (*V4L2Camera, error) {
	w, h, err := parseRes(resolution)
	if err != nil {
		return nil, err
	}
	return &V4L2Camera{devPath: devPath, w: w, h: h}, nil
}

func parseRes(s string) (uint32, uint32, error) {
	parts := strings.SplitN(s, "x", 2)
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("bad resolution %q", s)
	}
	w, err1 := strconv.Atoi(parts[0])
	h, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil {
		return 0, 0, fmt.Errorf("bad resolution %q", s)
	}
	return uint32(w), uint32(h), nil
}

func (c *V4L2Camera) Capture() ([]byte, error) {
	dev, err := device.Open(c.devPath,
		device.WithPixFormat(v4l2.PixFormat{
			PixelFormat: v4l2.PixelFmtMJPEG, Width: c.w, Height: c.h,
		}))
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", c.devPath, err)
	}
	defer dev.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := dev.Start(ctx); err != nil {
		return nil, err
	}

	var frame []byte
	const warmup = 2
	for i := 0; i <= warmup; i++ {
		f, ok := <-dev.GetOutput()
		if !ok {
			return nil, fmt.Errorf("camera stream closed")
		}
		frame = f
	}
	if len(frame) == 0 {
		return nil, fmt.Errorf("empty frame")
	}
	out := make([]byte, len(frame))
	copy(out, frame)
	return out, nil
}
```

- [ ] **Step 2: Add dependency and verify build**

Run: `go get github.com/vladimirvivien/go4vl@latest && go build ./internal/hw/real/`
Expected: builds.

- [ ] **Step 3: On-device verification (Pi only)**

Confirmed via the camera publisher in Plan 3; for now, a temporary `main`
harness or test on the Pi that writes `Capture()` output to a `.jpg` and opens
it.

---

### Task 9: GPIO button FSM (+ single/double-press, unit-tested)

**Depends on:** Task 1

**Files:**
- Create: `internal/hw/real/button.go`
- Test: `internal/hw/real/button_test.go`

The press-detection state machine (single vs. double within a window) is pure
logic driven by press timestamps, so it is fully unit-tested; only the GPIO edge
source is hardware.

- [ ] **Step 1: Write the failing FSM test**

`internal/hw/real/button_test.go`:
```go
package real

import (
	"testing"
	"time"

	"github.com/iot-root/garden-of-eden/internal/hw"
)

func collect(presses []time.Duration, window time.Duration) []hw.ButtonEvent {
	d := newPressDetector(window)
	var out []hw.ButtonEvent
	var t time.Time
	for _, gap := range presses {
		t = t.Add(gap)
		if ev, ok := d.press(t); ok {
			out = append(out, ev)
		}
	}
	if ev, ok := d.flush(t.Add(window + time.Millisecond)); ok {
		out = append(out, ev)
	}
	return out
}

func TestSinglePress(t *testing.T) {
	got := collect([]time.Duration{0}, 300*time.Millisecond)
	if len(got) != 1 || got[0] != hw.SinglePress {
		t.Errorf("got %v, want [SinglePress]", got)
	}
}

func TestDoublePress(t *testing.T) {
	got := collect([]time.Duration{0, 100 * time.Millisecond}, 300*time.Millisecond)
	if len(got) != 1 || got[0] != hw.DoublePress {
		t.Errorf("got %v, want [DoublePress]", got)
	}
}

func TestTwoSinglesWhenFarApart(t *testing.T) {
	got := collect([]time.Duration{0, 500 * time.Millisecond}, 300*time.Millisecond)
	if len(got) != 2 || got[0] != hw.SinglePress || got[1] != hw.SinglePress {
		t.Errorf("got %v, want [SinglePress SinglePress]", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/hw/real/ -run Press -v`
Expected: build failure — `undefined: newPressDetector`.

- [ ] **Step 3: Implement the FSM and the GPIO-backed button**

`internal/hw/real/button.go`:
```go
package real

import (
	"time"

	"github.com/iot-root/garden-of-eden/internal/hw"
	"github.com/warthog618/go-gpiocdev"
)

// pressDetector turns press timestamps into single/double-press gestures.
type pressDetector struct {
	window  time.Duration
	pending bool
	first   time.Time
}

func newPressDetector(window time.Duration) *pressDetector {
	return &pressDetector{window: window}
}

// press records a press at time t. If it completes a double-press, it returns
// (DoublePress, true) immediately. Otherwise it buffers and returns ok=false;
// call flush after the window to emit a buffered single-press.
func (d *pressDetector) press(t time.Time) (hw.ButtonEvent, bool) {
	if d.pending && t.Sub(d.first) <= d.window {
		d.pending = false
		return hw.DoublePress, true
	}
	// If a previous press is pending but expired, emit it as single first.
	var emit bool
	var ev hw.ButtonEvent
	if d.pending {
		ev, emit = hw.SinglePress, true
	}
	d.pending = true
	d.first = t
	if emit {
		return ev, true
	}
	return 0, false
}

// flush emits a buffered single-press if its window has elapsed by time t.
func (d *pressDetector) flush(t time.Time) (hw.ButtonEvent, bool) {
	if d.pending && t.Sub(d.first) > d.window {
		d.pending = false
		return hw.SinglePress, true
	}
	return 0, false
}

// GPIOButton implements hw.Button on a falling-edge button with debounce.
type GPIOButton struct {
	ch   chan hw.ButtonEvent
	line *gpiocdev.Line
	det  *pressDetector
}

func NewGPIOButton(chip string, gpio int, window, debounce time.Duration) (*GPIOButton, error) {
	b := &GPIOButton{ch: make(chan hw.ButtonEvent, 4), det: newPressDetector(window)}
	line, err := gpiocdev.RequestLine(chip, gpio,
		gpiocdev.WithPullUp,
		gpiocdev.WithFallingEdge,
		gpiocdev.DebounceOption(debounce),
		gpiocdev.WithEventHandler(func(gpiocdev.LineEvent) {
			if ev, ok := b.det.press(time.Now()); ok {
				b.emit(ev)
			}
		}))
	if err != nil {
		return nil, err
	}
	b.line = line
	go b.flusher(window)
	return b, nil
}

func (b *GPIOButton) flusher(window time.Duration) {
	ticker := time.NewTicker(window)
	defer ticker.Stop()
	for now := range ticker.C {
		if ev, ok := b.det.flush(now); ok {
			b.emit(ev)
		}
	}
}

func (b *GPIOButton) emit(ev hw.ButtonEvent) {
	select {
	case b.ch <- ev:
	default:
	}
}

func (b *GPIOButton) Events() <-chan hw.ButtonEvent { return b.ch }
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/hw/real/ -run Press -v`
Expected: PASS.

> Note: `DebounceOption`/pull/edge option names follow go-gpiocdev; if the
> pinned version differs, adjust to the equivalent (`gpiocdev.WithDebounce`).
> The FSM (the tested part) is unaffected.

---

### Task 10: `real.New` factory

**Depends on:** Tasks 2–9

**Files:**
- Create: `internal/hw/real/real.go`

Builds a `hw.Devices` from config, tolerating individual sensor init failures by
leaving that field nil (mirrors the Python "Failed to initiate sensor" path).

- [ ] **Step 1: Implement the factory**

`internal/hw/real/real.go`:
```go
package real

import (
	"log"
	"time"

	"github.com/iot-root/garden-of-eden/internal/config"
	"github.com/iot-root/garden-of-eden/internal/hw"
)

const gpioChip = "gpiochip0"

// New constructs real hardware. Light and pump are required (error if they
// fail). Sensors degrade gracefully to nil with a logged warning.
func New(cfg config.Config) (hw.Devices, func(), error) {
	var d hw.Devices
	var closers []func()

	light, err := NewLightPWM()
	if err != nil {
		return d, nil, err
	}
	d.Light = light

	pump, err := NewPumpPWM(gpioChip, 24, 50)
	if err != nil {
		return d, nil, err
	}
	d.Pump = pump
	closers = append(closers, func() { _ = pump.Close() })

	d.Distance = NewHCSR04(gpioChip, 26, 19)

	bus, err := OpenBus()
	if err != nil {
		log.Printf("i2c bus unavailable: %v (sensors disabled)", err)
	} else {
		closers = append(closers, func() { _ = bus.Close() })
		switch cfg.Device.Model {
		default:
			// SENSOR_TYPE selection preserved via env in Plan 3 config; default AM2320.
			d.Env = NewEnvAM2320(bus)
		}
		if pcb, err := NewPCT2075(bus, gpioChip, 25); err != nil {
			log.Printf("pct2075 init failed: %v", err)
		} else {
			d.PCBTemp = pcb
		}
		d.Power = NewINA219(bus)
	}

	if cam, err := NewV4L2Camera(cfg.Camera.UpperDevice, cfg.Camera.Resolution); err != nil {
		log.Printf("upper camera init failed: %v", err)
	} else {
		d.UpperCamera = cam
	}
	if cam, err := NewV4L2Camera(cfg.Camera.LowerDevice, cfg.Camera.Resolution); err != nil {
		log.Printf("lower camera init failed: %v", err)
	} else {
		d.LowerCamera = cam
	}

	if btn, err := NewGPIOButton(gpioChip, 13, time.Second, 200*time.Millisecond); err != nil {
		log.Printf("button init failed: %v", err)
	} else {
		d.Button = btn
	}

	cleanup := func() {
		for i := len(closers) - 1; i >= 0; i-- {
			closers[i]()
		}
	}
	return d, cleanup, nil
}
```

- [ ] **Step 2: Add camera/sensor config fields**

Append to `internal/config/config.go` `Config` struct and `defaults()`:
```go
// in the type list:
type CameraConfig struct {
	UpperDevice string `yaml:"upper_device"`
	LowerDevice string `yaml:"lower_device"`
	Resolution  string `yaml:"resolution"`
}

// add field to Config:
//   Camera CameraConfig `yaml:"camera"`
//   SensorType string   `yaml:"sensor_type"`
```
In `defaults()` add:
```go
Camera: CameraConfig{UpperDevice: "/dev/video0", LowerDevice: "/dev/video2", Resolution: "640x480"},
```
In `applyEnv` add:
```go
envStr(&c.Camera.UpperDevice, "UPPER_CAMERA_DEVICE")
envStr(&c.Camera.LowerDevice, "LOWER_CAMERA_DEVICE")
envStr(&c.Camera.Resolution, "CAMERA_RESOLUTION")
envStr(&c.SensorType, "SENSOR_TYPE")
```
Then in `real.New`, select the env sensor by `cfg.SensorType` (`"DHT20"` →
`NewEnvAHT20(bus)`, else `NewEnvAM2320(bus)`), replacing the `switch` above.

- [ ] **Step 3: Verify build and existing tests**

Run: `go build ./... && go test ./internal/config/ ./internal/hw/...`
Expected: builds; config + hw tests PASS.

---

### Task 11: Wire `--hw=real`, sensor REST routes, run suite, commit

**Depends on:** Task 10, plus Plan 1 httpapi/main

**Files:**
- Modify: `internal/httpapi/httpapi.go`
- Modify: `cmd/gardynd/main.go`
- Test: `internal/httpapi/httpapi_test.go` (append)

- [ ] **Step 1: Write the failing sensor-route test (append)**

Append to `internal/httpapi/httpapi_test.go`:
```go
func TestDistanceRoute(t *testing.T) {
	devs := mock.New()
	c := core.New(devs)
	go c.Run()
	defer c.Stop()
	h := HandlerWithSensors(c, devs)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/distance", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "distance") {
		t.Errorf("distance route: %d %s", rec.Code, rec.Body.String())
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/httpapi/ -run Distance -v`
Expected: build failure — `undefined: HandlerWithSensors`.

- [ ] **Step 3: Add sensor routes**

In `internal/httpapi/httpapi.go`, refactor so `Handler(c)` calls
`HandlerWithSensors(c, hw.Devices{})` and add the sensor handler:
```go
import "github.com/iot-root/garden-of-eden/internal/hw"

func Handler(c *core.Core) http.Handler { return HandlerWithSensors(c, hw.Devices{}) }

// HandlerWithSensors adds read-only sensor GET routes (parity with Flask).
func HandlerWithSensors(c *core.Core, d hw.Devices) http.Handler {
	mux := baseControlMux(c) // the *http.ServeMux built in Plan 1

	if d.Distance != nil {
		mux.HandleFunc("GET /distance", func(w http.ResponseWriter, _ *http.Request) {
			v, err := d.Distance.MeasureCM()
			if err != nil {
				writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, map[string]float64{"distance": v})
		})
	}
	if d.Env != nil {
		mux.HandleFunc("GET /temperature", func(w http.ResponseWriter, _ *http.Request) {
			t, _, err := d.Env.Read()
			sensorFloat(w, "temperature", t, err)
		})
		mux.HandleFunc("GET /humidity", func(w http.ResponseWriter, _ *http.Request) {
			_, h, err := d.Env.Read()
			sensorFloat(w, "humidity", h, err)
		})
	}
	if d.PCBTemp != nil {
		mux.HandleFunc("GET /pcb-temp", func(w http.ResponseWriter, _ *http.Request) {
			t, err := d.PCBTemp.Temperature()
			sensorFloat(w, "pcb-temp", t, err)
		})
	}
	if d.Power != nil {
		mux.HandleFunc("GET /pump/stats", func(w http.ResponseWriter, _ *http.Request) {
			r, err := d.Power.Read()
			if err != nil {
				writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, r)
		})
	}
	return mux
}

func sensorFloat(w http.ResponseWriter, key string, v float64, err error) {
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{key: fmt.Sprintf("%.2f", v)})
}
```
Refactor the Plan 1 `Handler` body into `func baseControlMux(c *core.Core) *http.ServeMux`
returning the mux (so both entry points share it), and add `"fmt"` to imports.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/httpapi/ -v`
Expected: PASS.

- [ ] **Step 5: Wire `--hw=real` in main**

In `cmd/gardynd/main.go`, replace the `case "real":` fatal with:
```go
case "real":
	var cleanup func()
	devs, cleanup, err = real.New(cfg)
	if err != nil {
		log.Fatalf("hardware init: %v", err)
	}
	defer cleanup()
```
Add imports `"github.com/iot-root/garden-of-eden/internal/hw/real"`, declare
`var err error` appropriately, and change the HTTP handler to
`httpapi.HandlerWithSensors(c, devs)`.

- [ ] **Step 6: Build, cross-compile, full suite**

Run: `make tidy && make build && make build-pi && go test ./...`
Expected: both binaries build; all tests PASS.

- [ ] **Step 7: On-device smoke (Pi only)**

Deploy `bin/gardynd-armv6`, run `--hw=real`, exercise light/pump and each sensor
GET route; confirm plausible values.

- [ ] **Step 8: Commit (single commit)**

Run:
```
git add internal/ cmd/ go.mod go.sum
git commit -m "feat: real Pi hardware drivers (PWM, HC-SR04, I2C sensors, V4L2, button) + sensor REST routes"
```

---

## Self-Review

**Spec coverage:** light hardware PWM ✓; pump soft-PWM ✓; HC-SR04 with
median ✓; AM2320/AHT20 env ✓ (SensorType select); PCT2075 + over-temp pin ✓;
INA219 power ✓; V4L2 cameras ✓; GPIO13 button single/double ✓; graceful
sensor-nil degradation ✓; sensor REST parity ✓; ARMv6 cross-compile retained ✓.
Deferred to Plan 3: publishers, discovery for sensors, scheduler, interlocks,
over-temp action, camera publishing. None of this plan's scope is unaddressed.

**Placeholder scan:** None. The only narrative notes (go-gpiocdev option names,
SensorType select) point at concrete alternatives, not unfinished work.

**Type consistency:** New interfaces (`DistanceSensor.MeasureCM`,
`EnvSensor.Read`, `PCBTempSensor.Temperature/OverTemp`, `PowerSensor.Read`
returning `hw.PowerReading`, `Button.Events`→`hw.ButtonEvent`) are implemented
with matching signatures in both `real` and `mock`. `round2` is defined once
(distance.go) and reused by power.go (same package). `config.CameraConfig` /
`SensorType` added in Task 10 are consumed in `real.New` and `applyEnv`
consistently. `HandlerWithSensors(c, hw.Devices)` is used by both the test and
main.

**Dependency audit:** Task 1 modifies `hw.go`/`mock.go`; every other task uses
those types → all depend on Task 1 (declared). Tasks 6 and 7 use `Bus` from
Task 5 (declared). Task 10 depends on the driver Tasks 2–9 (declared). Task 11
depends on Task 10 + Plan 1 files it modifies (declared). Tasks 2,3,4,8,9 each
create disjoint files under `internal/hw/real/` and depend only on Task 1 →
safe to parallelize; Tasks 5→6,7 serialize on `Bus`. No false `none` markings.
