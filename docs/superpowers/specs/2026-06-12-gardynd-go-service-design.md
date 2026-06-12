# gardynd — Single Contained Go Service

**Date:** 2026-06-12
**Status:** Approved design, ready for implementation planning
**Branch:** `go-service-rewrite`
**Revision:** 2026-06-12 — MQTT removed; gardynd is now REST-only. Home Assistant
integration happens through the dedicated custom integration (separate spec),
which is the sole HA front end.

## 1. Goal

Replace the two Python processes (`run.py` Flask API + `mqtt.py` MQTT
controller) with a single statically-linked Go binary, `gardynd`, that runs on
the stock Raspberry Pi Zero inside a Gardyn and exposes **one local HTTP/REST
API** for control, state, schedules, and camera frames.

The binary is cross-compiled with `GOOS=linux GOARCH=arm GOARM=6
CGO_ENABLED=0` so a single artifact runs on the original Pi Zero (ARMv6) and
everything newer.

### Runtime dependencies

| Today | After |
| --- | --- |
| Python venv + ~40 pip packages | none (static binary) |
| `pigpiod` daemon | removed — GPIO/PWM done in-process |
| `fswebcam` subprocess | removed — native V4L2 capture |
| `mosquitto` broker | **removed — no broker, REST only** |
| Home Assistant for scheduling | removed — scheduler runs on-device |

Deploy = one binary + one systemd unit + one YAML config file. **Nothing
external is required at runtime.**

### Home Assistant integration

gardynd no longer speaks MQTT. Home Assistant connects through a dedicated
**custom integration** (separate project/spec) that polls the gardynd REST API
and renders the light, pump, sensors, cameras, and schedule controls. The REST
API defined here is the stable contract that integration consumes.

## 2. Scope

### In scope

- A single local REST API (see §4.1) covering device control, a full `/state`
  snapshot, schedule CRUD, and camera frames.
- Native drivers replacing pigpiod/fswebcam: light PWM, pump PWM, HC-SR04
  distance, GPIO button, I2C sensors (AM2320/AHT20, PCT2075, INA219), V4L2
  cameras.
- On-device scheduler for light/pump cycles, persisted to config, controllable
  via REST. Replaces the Home Assistant automations.
- Edge features, all four confirmed in scope: INA219 pump power stats, camera
  capture, PCT2075 over-temp safety monitor, physical button.
- Reliability features: pump max-runtime failsafe, scheduler restart catch-up.
- Optional zeroconf/mDNS advertisement (`_gardynd._tcp`) so the HA integration
  can auto-discover the service.

### Out of scope (separate follow-up project)

- The Home Assistant custom integration itself. This design commits only to the
  **service-side REST contract** (§4.1) that the integration consumes. The
  integration has its own spec.

### Explicitly dropped

- MQTT entirely (paho client, HA MQTT discovery, retained state, LWT
  availability) — superseded by REST + the HA integration's polling.
- pigpiod, fswebcam, mosquitto, the Python venv, and (after parity) the Python
  code itself.

## 3. Approach

New branch `go-service-rewrite` off `main`, **in this repository**. The Python
code stays in place during bring-up so the two can run side-by-side on the Pi
(gardynd on an alternate `--http-port`). Once parity is verified, the systemd
units are switched and a final commit removes the Python service. This keeps the
plants safe (rollback at every step) while ending at a clean single-service repo.

## 4. Architecture

### 4.1 REST API (the HA-integration contract)

All endpoints are local HTTP on `:5000` (configurable). JSON unless noted.

| Method & path | Purpose |
| --- | --- |
| `GET /state` | Full snapshot: light, pump, sensors, water, over-temp, schedule-enabled flags, availability/uptime. The integration polls this. |
| `GET /healthz` | Liveness probe. |
| `POST /light/on` · `/light/off` | Light control. |
| `POST /light/brightness` `{"value":0-100}` | Set brightness. |
| `POST /pump/on` · `/pump/off` | Pump control (subject to water-low interlock). |
| `POST /pump/speed` `{"value":0-100}` | Set pump speed. |
| `GET /distance` · `/temperature` · `/humidity` · `/pcb-temp` · `/pump/stats` | On-demand sensor reads (Flask parity). |
| `GET /schedules` | Both channels' schedules. |
| `PUT /schedules/{channel}` | Replace a channel's schedule (entries + enabled); persists atomically. |
| `POST /schedule/{channel}/enabled` `{"enabled":bool}` | Toggle a channel's scheduler. |
| `POST /water/low-threshold` `{"cm":float}` | Set/clear the water-low interlock threshold; persists. |
| `GET /camera/upper.jpg` · `/camera/lower.jpg` | Latest JPEG frame (binary), for HA `Camera` entities. |

`GET /state` shape (illustrative):
```json
{
  "available": true,
  "uptime_s": 12345,
  "light": { "on": true, "brightness": 70 },
  "pump":  { "on": false, "speed": 100 },
  "sensors": {
    "temperature_c": 22.5, "humidity_pct": 55.0,
    "pcb_temp_c": 31.2, "water_level_cm": 7.4,
    "pump": { "bus_voltage": 12.0, "current": 0.5, "power": 6.0 }
  },
  "water": { "low_threshold_cm": 10.0, "low": false },
  "overtemp": false,
  "schedules": { "light": { "enabled": true }, "pump": { "enabled": true } }
}
```
Sensor fields are `null` when the sensor is absent/failed (the integration maps
`null` to an unavailable HA entity).

### 4.2 Package layout

```
cmd/gardynd/main.go          wiring, flags (--config, --hw=real|mock, --http-port)
internal/config/             YAML config load/save (atomic), .env-compatible
                             env-var overrides for migration
internal/hw/                 driver interfaces + real implementations
    light.go                 hardware PWM via sysfs (GPIO18, 8 kHz)
    pump.go                  soft-PWM goroutine (GPIO24, 50 Hz)
    distance.go              HC-SR04, kernel-timestamped edges, median-of-10
    button.go                GPIO13 debounce, single/double-press
    i2c/...                  AM2320/AHT20, PCT2075, INA219
    camera.go                V4L2 JPEG capture (warm-up frame skip)
    mock/                    in-memory fakes of every interface
internal/core/               single-writer state machine, scheduler, interlocks
internal/state/              thread-safe snapshot store serialized by GET /state
internal/httpapi/            REST handlers (control, /state, /schedules, /camera)
internal/discovery/          optional zeroconf advertiser (_gardynd._tcp)
```

### 4.3 Key libraries (all pure Go, CGO-free)

- `github.com/warthog618/go-gpiocdev` — GPIO lines + event timestamps.
- Kernel `pwm` sysfs for the light (one-time `dtoverlay=pwm` in config.txt).
- `periph.io/x/conn` + `/dev/i2c-1` — I2C sensors.
- `github.com/vladimirvivien/go4vl` — V4L2 camera capture.
- `github.com/grandcat/zeroconf` — optional mDNS advertisement.
- Standard library `net/http` for the API. No MQTT dependency.

### 4.4 Concurrency model — single writer + snapshot store

One `core` goroutine owns all hardware-mutating state. Every input source — REST
handler, button callback, scheduler tick — submits a command over a channel
rather than touching hardware directly. After applying a command (and on every
periodic sensor read), the core writes the resulting values into a thread-safe
**snapshot store** (`internal/state`). `GET /state` serializes that store under a
read lock. This replaces the Python module-level globals mutated from multiple
threads and makes the safety interlocks race-free by construction.

### 4.5 Data flow (pump-on example)

```
REST POST /pump/on   ─┐
button double-press  ─┼─> core command channel ─> core goroutine:
scheduler "pump on"  ─┘     1. check water-low interlock
                            2. hw.Pump.SetSpeed(speed)
                            3. arm max-runtime failsafe
                            4. update snapshot store (pump.on=true)
HA integration ──> GET /state (polled) ──> reads snapshot store
```

## 5. Core behavior

### 5.1 Scheduler (state-based, not edge-triggered)

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
```

> Note: `action` values are quoted strings. YAML parses bare `on`/`off` as
> booleans, so the loader treats them as strings (quoted in files; the parser
> accepts boolean coercion defensively).

On startup and after every restart the scheduler computes the **state the
timeline implies right now** (the most recent past entry per channel, wrapping
midnight) and applies it. A reboot at 06:05 restores "light on at 70%" rather
than waiting for the next edge.

Per-channel `enabled` is exposed over REST (`/schedules`, `/schedule/{channel}/enabled`).
When disabled, manual control (REST/button) is fully in effect and the scheduler
leaves that channel alone. Schedule CRUD (`GET/PUT /schedules`) persists
atomically to the YAML file and is the stable contract the HA integration
consumes.

### 5.2 Water-low interlock (enforced in core)

Before any pump-on from **any** source, if a water-low threshold is configured,
the core takes a distance reading; if water is too low it aborts the pump,
flashes the lights (ported behavior), and sets `water.low=true` in the snapshot.
This closes a current gap where the REST `/pump/on` path bypasses the check that
only the Python MQTT path performs.

### 5.3 Over-temp safety monitor

The PCT2075 alert pin (GPIO25) is wired into the running service (today
`over_temp_monitor.py` never runs in production). Reflected as `overtemp` in the
snapshot. A config flag `overtemp.cut_light` (default `false`) optionally drops
the light PWM while the alert is active; default is report-only.

### 5.4 Availability

`GET /state` includes `available` and `uptime_s`. The HA integration marks all
entities unavailable when a poll fails or the service is down (its
`DataUpdateCoordinator` handles this), replacing the MQTT LWT mechanism.

## 6. Error handling

- **Sensor read failure:** log, leave the snapshot field `null`/stale-flagged;
  the HC-SR04 reinitialize-on-failure recovery is ported.
- **Process crash:** systemd `Restart=always`; the integration shows
  unavailable until polls resume.
- **Pump failsafe:** configurable max continuous runtime (default 10 min); if
  exceeded without an off command, core forces the pump off and logs. Scheduler
  off-entries are unaffected.
- **Camera capture failure:** log and serve the last good frame (or 503 if none
  yet); never blocks the core.

## 7. Testing

- **Unit (against `hw/mock`):** scheduler timeline math (restart catch-up,
  midnight wrap); all pump-on interlock paths; HC-SR04 median-of-10; AM2320 CRC;
  config atomic save/load; snapshot-store concurrency.
- **REST:** `net/http/httptest` against the handler layer with mock hardware —
  golden-file assertions on `GET /state`, control round-trips reflected in the
  snapshot, `/schedules` CRUD persistence, `/camera/*.jpg` content type.
- **Laptop dev loop:** `gardynd --hw=mock` runs the entire service with fake
  hardware, so the HA integration can be developed against a real gardynd API
  before touching the Pi.

## 8. Migration & ops

1. Branch `go-service-rewrite` off `main`; Python remains until parity.
2. **Bring-up:** run `gardynd --hw=real --http-port 5001` alongside the live
   Python service; exercise control + `/state`.
3. **Cutover:** install the gardynd systemd unit; stop the Python `mqtt.service`;
   drop the `pigpiod` requirement; remove the venv. mosquitto is no longer
   needed by gardynd (uninstall only if nothing else uses it).
4. **One-time host change:** add `dtoverlay=pwm` to `/boot/config.txt` for
   hardware PWM on the light (documented in README).
5. **Cleanup commit:** delete `run.py`, `mqtt.py`, `app/`, `requirements.txt`,
   the pigpiod dependency; replace the `automations/*.yml` with a note pointing
   at on-device schedules + the HA integration; update README.
6. **CI:** Makefile + GitHub Actions cross-compile the ARMv6 binary and attach
   it to releases.

## 9. Follow-up projects

- **Home Assistant custom integration** — the sole HA front end. Polls
  `GET /state` via a `DataUpdateCoordinator`, exposes light/pump/sensors/cameras
  as entities, and provides schedule editing via a `set_schedule` service action
  backed by `PUT /schedules/{channel}`. Config flow + optional zeroconf setup.
  Separate spec.
