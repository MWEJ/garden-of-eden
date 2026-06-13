# gardyn — Home Assistant Custom Integration

**Date:** 2026-06-12
**Status:** Approved design, ready for implementation planning
**Branch:** `ha-integration` (new, off `main`)
**Depends on:** the gardynd REST contract in
`docs/superpowers/specs/2026-06-12-gardynd-go-service-design.md` §4.1.

## 1. Goal

A HACS-installable Home Assistant custom integration (domain `gardyn`) that is
the **sole** Home Assistant front end to a gardynd-controlled Gardyn. It speaks
only gardynd's local REST API — no MQTT, no cloud — exposing the light, pump,
sensors, cameras, and schedule controls as native HA entities and services, with
the schedules running autonomously on the Pi.

## 2. Scope

### In scope

- One config entry per Gardyn; config flow (manual host/port) + zeroconf
  auto-discovery (`_gardynd._tcp`); options flow for poll interval.
- A `DataUpdateCoordinator` polling `GET /state` (~15 s default).
- Entity platforms: `light`, `fan` (pump), `sensor`, `binary_sensor`, `switch`
  (schedule enable), `number` (water threshold), `camera`.
- A `gardyn.set_schedule` service writing schedule entries via
  `PUT /schedules/{channel}`, plus read-only schedule sensors exposing the
  active timeline as attributes.
- Availability driven by coordinator success (replaces MQTT LWT).
- Tests via `pytest-homeassistant-custom-component`.

### Out of scope

- A custom Lovelace drag-and-drop schedule card (future nicety; the
  `set_schedule` service + schedule-sensor attributes are fully functional now).
- Any gardynd-side change beyond the REST contract already specced. If a small
  gap is found during implementation (e.g., an endpoint shape), it is raised
  against the gardynd spec, not worked around here.
- Publishing to the HACS default store (can be done later by splitting to its
  own repo; this spec only requires HACS-from-repo installability).

## 3. Architecture

### 3.1 Component layout (HA custom component conventions)

```
custom_components/gardyn/
  __init__.py          async_setup_entry / unload; creates api + coordinator;
                       forwards to platforms
  manifest.json        domain, version, iot_class=local_polling, zeroconf,
                       requirements, codeowners
  const.py             DOMAIN, defaults, platform list
  api.py               GardyndClient — async aiohttp REST wrapper
  coordinator.py       GardynCoordinator(DataUpdateCoordinator) polling /state
  config_flow.py       user + zeroconf + options flows
  entity.py            GardynEntity base (device_info, availability)
  light.py             pump-independent light platform
  fan.py               pump platform (on/off + percentage)
  sensor.py            env/pcb/water/power + schedule sensors
  binary_sensor.py     water_low, overtemp
  switch.py            light/pump schedule-enable switches
  number.py            water-low threshold
  camera.py            upper/lower cameras
  services.yaml        set_schedule schema
  strings.json
  translations/en.json
hacs.json              HACS metadata (name, HA min version)
```

### 3.2 API client (`api.py`)

`GardyndClient(host, port, session)` exposes typed async methods:

| Method | gardynd call |
| --- | --- |
| `get_state() -> dict` | `GET /state` |
| `get_schedules() -> dict` | `GET /schedules` |
| `light_on()/off()/set_brightness(pct)` | `POST /light/...` |
| `pump_on()/off()/set_speed(pct)` | `POST /pump/...` |
| `set_schedule(channel, schedule: dict)` | `PUT /schedules/{channel}` |
| `set_schedule_enabled(channel, on)` | `POST /schedule/{channel}/enabled` |
| `set_water_threshold(cm)` | `POST /water/low-threshold` |
| `camera_url(which)` | builds `/camera/{which}.jpg` URL |
| `healthz()` | `GET /healthz` (config-flow validation) |

All requests use a per-call timeout; non-2xx raises `GardyndApiError`.

### 3.3 Coordinator (`coordinator.py`)

`GardynCoordinator` calls `client.get_state()` each interval and, in the same
cycle, `client.get_schedules()` (one extra cheap local call) so the schedule
sensors can expose entries; it merges both into one snapshot dict. On any error
it raises `UpdateFailed`, marking every entity unavailable. That merged snapshot
is the single source of truth all platforms read. After a control call, the
calling entity does an optimistic local state set and calls
`async_request_refresh()` to reconcile with gardynd.

### 3.4 Entities

All entities subclass `GardynEntity`, which sets `device_info` (identifier-keyed,
so everything groups under one HA device) and ties `available` to
`coordinator.last_update_success` (and to the relevant `/state` field being
non-null).

| Platform | Entity(ies) | `/state` source | Control |
| --- | --- | --- | --- |
| light | Light (brightness) | `light.{on,brightness}` | `/light/on,off,brightness` |
| fan | Pump | `pump.{on,speed}` | `/pump/on,off,speed` (percentage) |
| sensor | Temperature, Humidity, PCB Temperature, Water Level, Pump Voltage, Pump Current, Pump Power | `sensors.*` | — |
| sensor | Light Schedule, Pump Schedule (state = entry count; attrs = entries) | `/schedules` (fetched in coordinator or on demand) | — |
| binary_sensor | Water Low, Over-Temp (`device_class: problem`) | `water.low`, `overtemp` | — |
| switch | Light Schedule Enabled, Pump Schedule Enabled | `schedules.{ch}.enabled` | `/schedule/{ch}/enabled` |
| number | Water-Low Threshold (cm, 0–15, step 0.5) | `water.low_threshold_cm` | `/water/low-threshold` |
| camera | Upper Camera, Lower Camera | `/camera/{which}.jpg` | — |

A `null` sensor field maps to an unavailable entity (gardynd reports `null` when
a sensor is absent/failed). The schedule sensors require the schedule list; the
coordinator fetches `/schedules` alongside `/state` (or the schedule sensors pull
it on update) — see §6 note.

### 3.5 Service: `gardyn.set_schedule`

`services.yaml` schema:
```yaml
set_schedule:
  fields:
    config_entry: { required: true, selector: { config_entry: { integration: gardyn } } }
    channel:      { required: true, selector: { select: { options: [light, pump] } } }
    enabled:      { required: false, selector: { boolean: {} } }
    entries:
      required: true
      selector: { object: {} }   # list of {at: "HH:MM", action: on|off, brightness?: 0-100}
```
Handler validates each entry (`at` matches `HH:MM`, `action ∈ {on, off}`,
`brightness ∈ 0..100` when present), builds the schedule object, and calls
`client.set_schedule(channel, schedule)`. On success it refreshes the
coordinator so the schedule sensors/switches update.

## 4. Setup & discovery

- **User flow:** form for `host` + `port` (default 5000) → validate via
  `healthz()` then `get_state()` to read `identifier` → set as `unique_id`
  (abort if already configured) → create entry.
- **Zeroconf flow:** `manifest.json` registers the `_gardynd._tcp.local.` type;
  discovery prefills host/port; user confirms; same `identifier` unique_id guard.
- **Options flow:** edit poll interval (default 15 s, min 5 s).

## 5. Error handling

- API timeouts/connection errors → `GardyndApiError`; in the coordinator these
  become `UpdateFailed` (entities unavailable until polls recover).
- Control-call failures raise `HomeAssistantError` so the HA UI shows the error;
  the entity then requests a refresh to re-sync true state.
- Config-flow validation failures map to `cannot_connect` / `invalid_host` form
  errors.

## 6. Testing

`pytest-homeassistant-custom-component` against a mocked aiohttp gardynd:

- **Config flow:** manual happy path, `cannot_connect`, duplicate `unique_id`
  abort, zeroconf discovery → confirm.
- **Coordinator:** parses a `/state` JSON fixture; an HTTP error yields
  `UpdateFailed` and entities unavailable.
- **Platforms:** each entity's state + availability from the fixture, including
  `null` sensor → unavailable; light brightness and fan percentage scaling;
  number/switch issue the correct POST.
- **Service:** `set_schedule` validates entries and issues the expected
  `PUT /schedules/{channel}` body; bad entries raise without calling the API.

> Implementation note (schedule data): `/state` does not include full schedule
> entries (only `enabled` flags). The coordinator therefore also fetches
> `GET /schedules` each cycle (one extra cheap local call) so the schedule
> sensors can expose entries as attributes. If that proves wasteful, gardynd can
> fold an abbreviated schedule summary into `/state` — a change raised against
> the gardynd spec, not hacked around in the integration.

## 7. Packaging & ops

- Lives in this repo under `custom_components/gardyn/` with a root `hacs.json`,
  installable by adding the GitHub repo as a HACS custom repository.
- Branch `ha-integration` off `main`; developed against `gardynd --hw=mock`
  running locally so the integration can be exercised end-to-end without a Pi.
- README section documents HACS install, config flow, the `set_schedule`
  service, and the example schedule payload.

## 8. Relationship to gardynd

This integration consumes — and is gated on — the gardynd REST contract
(`/state`, `/light/*`, `/pump/*`, `/schedules`, `/schedule/{ch}/enabled`,
`/water/low-threshold`, `/camera/*.jpg`, `/healthz`, zeroconf `_gardynd._tcp`).
Those endpoints are defined and built in the gardynd plans (Plans 1–3). The
integration should be implemented after gardynd Plan 3 lands so it can be tested
against a real (mock-hardware) gardynd.
