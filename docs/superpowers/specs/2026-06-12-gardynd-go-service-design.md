# gardynd — Single Contained Go Service

**Date:** 2026-06-12
**Status:** Approved design, ready for implementation planning
**Branch:** `go-service-rewrite`

## 1. Goal

Replace the two Python processes (`run.py` Flask API + `mqtt.py` MQTT
controller) with a single statically-linked Go binary, `gardynd`, that runs on
the stock Raspberry Pi Zero inside a Gardyn.

The binary is cross-compiled with `GOOS=linux GOARCH=arm GOARM=6
CGO_ENABLED=0` so a single artifact runs on the original Pi Zero (ARMv6) and
everything newer.

### Runtime dependencies

| Today | After |
| --- | --- |
| Python venv + ~40 pip packages | none (static binary) |
| `pigpiod` daemon | removed — GPIO/PWM done in-process |
| `fswebcam` subprocess | removed — native V4L2 capture |
| Home Assistant for scheduling | optional — scheduler runs on-device |
| `mosquitto` broker | **still required** (unchanged) |

Deploy = one binary + one systemd unit + one YAML config file.

### Compatibility requirement (non-negotiable)

The service MUST preserve the existing MQTT topic tree, Home Assistant
discovery payloads, and every `unique_id`, so existing HA entities survive the
swap with no reconfiguration. The REST routes and port (`:5000`) are preserved
for the `bin/` shell scripts and direct callers.

## 2. Scope

### In scope

- REST API (parity with current Flask routes) **and** MQTT (parity with
  current `mqtt.py`), sharing one driver layer.
- Native drivers replacing pigpiod/fswebcam: light PWM, pump PWM, HC-SR04
  distance, GPIO button, I2C sensors (AM2320/AHT20, PCT2075, INA219), V4L2
  cameras.
- On-device scheduler for light/pump cycles, persisted to config, controllable
  via REST (CRUD) and MQTT (per-channel enable/disable). Replaces — but can
  coexist with — the Home Assistant automations.
- Edge features, all four confirmed in scope: INA219 pump power stats,
  camera capture, PCT2075 over-temp safety monitor, physical button.
- New reliability features: MQTT Last-Will availability, pump max-runtime
  failsafe, scheduler restart catch-up.

### Out of scope (separate follow-up project)

- A dedicated Home Assistant custom integration for rich schedule control.
  This design only commits to the **service-side API contract** (REST
  `/schedules` CRUD + MQTT schedule switches) that the integration will
  consume. The integration gets its own spec once the service API is stable.

### Explicitly dropped

- pigpiod, fswebcam, the Python venv, and (after parity) the Python code
  itself.

## 3. Approach

New branch `go-service-rewrite` off `main`, **in this repository**. The Python
code stays in place on the branch during bring-up so the two can run
side-by-side on the Pi (Go on an alternate topic prefix + HTTP port). Once
parity is verified in Home Assistant, the systemd units are switched and a
final commit removes the Python service, the pigpiod dependency, and updates
the README.

This keeps the plants safe (proven rollback at every step) while ending at a
clean single-service repo.

## 4. Architecture

### Package layout

```
cmd/gardynd/main.go          wiring, flags (--config, --hw=real|mock,
                             --topic-prefix, --http-port)
internal/config/             YAML config load/save (atomic), .env-compatible
                             env-var overrides for migration
internal/hw/                 driver interfaces + real implementations
    light.go                 hardware PWM via sysfs (GPIO18, 8 kHz)
    pump.go                  soft-PWM goroutine (GPIO24, 50 Hz)
    distance.go              HC-SR04, kernel-timestamped edges, median-of-10
    button.go                GPIO13 debounce, single/double-press
    i2c/am2320.go            temperature + humidity (AM2320 / AHT20 variants)
    i2c/pct2075.go           PCB temperature + over-temp alert
    i2c/ina219.go            pump bus voltage / current / power
    camera.go                V4L2 JPEG capture (warm-up frame skip)
    mock/                    in-memory fakes of every interface
internal/core/               device state machine, scheduler, interlocks
internal/mqttsvc/            paho client, HA discovery, command handlers,
                             periodic publishers, LWT availability
internal/httpapi/            REST handlers (existing routes + /schedules + /healthz)
```

### Key libraries (all pure Go, CGO-free)

- `github.com/warthog618/go-gpiocdev` — GPIO lines + event timestamps (pump
  soft-PWM, HC-SR04, button) via the modern `/dev/gpiochip` cdev interface.
- Hardware PWM for the light via the kernel `pwm` sysfs interface (requires a
  one-time `dtoverlay=pwm` in `/boot/config.txt`).
- `periph.io/x/conn` + `/dev/i2c-1` — I2C sensors.
- `github.com/eclipse/paho.mqtt.golang` — MQTT client with auto-reconnect.
- `github.com/vladimirvivien/go4vl` — V4L2 camera capture.
- `github.com/mochi-mqtt/server` — embedded broker, **test dependency only**.

### Concurrency model — single writer

One `core` goroutine owns all hardware-mutating state (light, pump, brightness,
speed, schedule-enabled flags, water-low threshold). Every input source — MQTT
handler, REST handler, button callback, scheduler tick — submits a command over
a channel rather than touching hardware directly. The core applies the command,
updates state, and publishes resulting MQTT state messages.

This replaces the Python module-level globals (`brightness`, `speed`,
`light_state`, `WATER_LOW_CM`) mutated from multiple threads, and makes the
safety interlocks race-free by construction.

### Data flow (pump-on example)

```
MQTT "pump/command ON" ─┐
REST POST /pump/on      ─┤
button double-press     ─┼─> core command channel ─> core goroutine:
scheduler "pump on"     ─┘                              1. check water-low interlock
                                                        2. hw.Pump.SetSpeed(speed)
                                                        3. arm max-runtime failsafe
                                                        4. publish gardyn/pump/state ON
```

## 5. Core behavior

### Scheduler (state-based, not edge-triggered)

Schedule entries live in the config file, one list per channel:

```yaml
schedules:
  light:
    enabled: true
    entries:
      - { at: "06:00", action: "on",  brightness: 70 }
      - { at: "09:00", action: "off" }
      - { at: "17:00", action: "on",  brightness: 50 }
      - { at: "20:00", action: "off" }
  pump:
    enabled: true
    entries:
      - { at: "09:30", action: "on" }
      - { at: "09:35", action: "off" }
      # ...
```

> Note: `action` values are quoted strings. YAML parses bare `on`/`off` as
> booleans, so the config loader must treat them as strings (quoted in files,
> and the parser should accept the boolean coercion defensively).

On startup and after every restart the scheduler computes the **state the
timeline implies right now** (the most recent past entry for each channel) and
applies it. A reboot at 06:05 therefore restores "light on at 70%", rather than
waiting for the next edge — fixing a latent gap in the current HA-edge model.

Per-channel `enabled` is exposed as a Home Assistant `switch` over MQTT and as
REST. When a channel is disabled, manual control (MQTT/REST/button) is fully in
effect and the scheduler leaves it alone.

Schedule CRUD over REST (`GET/PUT /schedules/{channel}`) persists atomically to
the YAML file and is the stable contract the future HA integration consumes.

### Water-low interlock (enforced in core)

Before any pump-on from **any** source, if a water-low threshold is configured,
the core takes a distance reading; if water is too low it aborts the pump,
flashes the lights (ported behavior), and publishes `water/low/state ON`. This
closes a current gap where the REST `/pump/on` path bypasses the check that
only the MQTT path performs.

### Over-temp safety monitor

The PCT2075 alert pin (GPIO25) is wired into the running service (today
`over_temp_monitor.py` is a standalone script that never runs in production).
Published as an HA `binary_sensor` (`device_class: problem`). A config flag
`overtemp.cut_light` (default `false`) optionally drops the light PWM while the
alert is active; default behavior is report-only.

### Availability (new)

The MQTT client registers a retained Last-Will on a `gardyn/availability` topic
(`online`/`offline`), referenced by `availability_topic` in every discovery
config. HA shows the device unavailable when the service stops, instead of
displaying stale values.

## 6. Error handling

- **Sensor read failure:** log and skip that publish cycle; the existing
  HC-SR04 reinitialize-on-failure recovery is ported.
- **MQTT disconnect:** paho auto-reconnect with backoff; on reconnect, re-send
  discovery + retained state.
- **Process crash:** systemd `Restart=always`.
- **Pump failsafe:** configurable max continuous runtime (default 10 min); if
  exceeded without an off command, core forces the pump off and logs. Scheduler
  off-entries are unaffected.
- **Camera capture failure:** log and retry on the next interval; never blocks
  other publishers (runs in its own goroutine).

## 7. Testing

- **Unit (against `hw/mock`):** scheduler timeline math including restart
  catch-up and midnight wraparound; all pump-on interlock paths; HC-SR04
  median-of-10 filter; AM2320 CRC validation; config atomic save/load.
- **REST:** `net/http/httptest` against the handler layer with mock hardware.
- **MQTT integration:** spin up an embedded `mochi-mqtt` broker in-test, assert
  discovery payloads match the current Python output byte-for-byte (golden
  files) and that command topics round-trip to state topics.
- **Laptop dev loop:** `gardynd --hw=mock` runs the entire service with fake
  hardware, so behavior can be exercised in a real Home Assistant before
  touching the Pi.

## 8. Migration & ops

1. Branch `go-service-rewrite` off `main`; Python remains until parity.
2. **Bring-up:** run `gardynd --topic-prefix gardyn2 --http-port 5001`
   alongside the live Python service; compare readings side-by-side in HA.
3. **Cutover:** replace `mqtt.service` `ExecStart` with the binary; drop the
   `pigpiod` requirement; remove the Python venv.
4. **One-time host change:** add `dtoverlay=pwm` to `/boot/config.txt` for
   hardware PWM on the light (documented in README).
5. **Cleanup commit:** delete `run.py`, `mqtt.py`, `app/`, `requirements.txt`,
   the venv reference, and the pigpiod dependency; keep `automations/*.yml`
   with a deprecation note until the HA-integration project lands; update
   README install steps.
6. **CI:** Makefile + GitHub Actions cross-compile the ARMv6 binary and attach
   it to releases.

## 9. Follow-up projects

- **Home Assistant custom integration** for first-class schedule editing
  (consumes the REST `/schedules` contract + MQTT schedule switches defined
  here). Separate spec.
