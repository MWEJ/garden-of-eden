# gardynd Plan 4 — Migration & Ops Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship gardynd: systemd unit, CI cross-compilation + release artifact, an example config, README install/migration docs, and removal of the now-replaced Python service once parity is confirmed.

**Architecture:** Deployment artifacts and docs only — no service logic changes. The Python removal is gated behind an explicit on-device parity checklist so the plants are never left uncontrolled.

**Tech Stack:** systemd, GitHub Actions, Make, Markdown.

**Spec:** `docs/superpowers/specs/2026-06-12-gardynd-go-service-design.md`
**Depends on:** Plans 1–3 (a complete, building `gardynd`).

---

## File Structure (this plan)

```
services/etc/systemd/system/gardynd.service   new unit
config.example.yaml                            documented sample config
.github/workflows/build.yml                    cross-compile + test + release
README.md                                      MODIFY: install + migration section
automations/README.md                          deprecation note
# Removed in Task 6 (after parity): run.py, mqtt.py, config.py, app/, requirements.txt,
#   services/etc/systemd/system/mqtt.service, tests/ (python), bin/*.sh that call python
```

---

### Task 1: systemd unit

**Depends on:** none (within this plan)

**Files:**
- Create: `services/etc/systemd/system/gardynd.service`

No `pigpiod` dependency (gone), no venv. Runs the binary directly with a config
path; restarts always.

- [ ] **Step 1: Write the unit**

`services/etc/systemd/system/gardynd.service`:
```ini
[Unit]
Description=gardynd — Gardyn controller (Go)
After=network-online.target
Wants=network-online.target

[Service]
User=gardyn
ExecStart=/usr/local/bin/gardynd --hw=real --config /etc/gardynd/config.yaml
Restart=always
RestartSec=3
# Hardware access: GPIO (gpiochip), I2C, PWM sysfs, V4L2 cameras.
SupplementaryGroups=gpio i2c video
AmbientCapabilities=CAP_SYS_RAWIO

[Install]
WantedBy=multi-user.target
```

- [ ] **Step 2: Lint the unit (host)**

Run: `systemd-analyze verify services/etc/systemd/system/gardynd.service || true`
Expected: no fatal errors (warnings about missing binary path on a non-Pi host
are fine).

---

### Task 2: Example config

**Depends on:** Plans 1–3 config schema

**Files:**
- Create: `config.example.yaml`

- [ ] **Step 1: Write the example**

`config.example.yaml`:
```yaml
http:
  port: 5000

device:
  identifier: gardyn-xx
  model: "gardyn 3.0"
  version: "1.0.0"

sensor_type: AM2320   # or DHT20

water:
  low_cm: 0           # 0 disables the low-water pump interlock

overtemp:
  cut_light: false    # true => cut the light while the PCB is over-temp

camera:
  upper_device: /dev/video0
  lower_device: /dev/video2
  resolution: 640x480
  interval_seconds: 3600

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
      - { at: "16:00", action: "on" }
      - { at: "16:05", action: "off" }
      - { at: "21:00", action: "on" }
      - { at: "21:05", action: "off" }
```

- [ ] **Step 2: Validate it loads**

Add a guarded test `internal/config/example_test.go`:
```go
package config

import "testing"

func TestExampleConfigLoads(t *testing.T) {
	c, err := Load("../../config.example.yaml")
	if err != nil {
		t.Fatalf("example config failed to load: %v", err)
	}
	if !c.Schedules.Light.Enabled || len(c.Schedules.Pump.Entries) == 0 {
		t.Errorf("example schedules not parsed: %+v", c.Schedules)
	}
	if c.Camera.Resolution != "640x480" {
		t.Errorf("camera resolution = %q", c.Camera.Resolution)
	}
}
```
Run: `go test ./internal/config/ -run Example -v`
Expected: PASS.

---

### Task 3: GitHub Actions — test + cross-compile + release

**Depends on:** Task in Plans 1–3 (buildable module)

**Files:**
- Create: `.github/workflows/build.yml`

- [ ] **Step 1: Write the workflow**

`.github/workflows/build.yml`:
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
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: "1.23"
      - name: Test
        run: go test ./...
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
```

- [ ] **Step 2: Validate YAML locally**

Run: `python3 -c "import yaml,sys; yaml.safe_load(open('.github/workflows/build.yml'))" && echo OK`
Expected: `OK`.

---

### Task 4: README install + migration docs

**Depends on:** Tasks 1–3

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Add a gardynd section**

Insert a new top section after the project intro covering exactly these points:
```markdown
## Running gardynd (Go service)

gardynd replaces the Python Flask + MQTT services with a single static binary
exposing one local REST API — no broker, no pigpiod, no fswebcam, no Python
venv. **Nothing external is required at runtime.** Home Assistant connects via
the dedicated gardynd custom integration (separate repo/release).

### One-time Pi setup
1. Enable I2C and hardware PWM in `/boot/config.txt` (or
   `/boot/firmware/config.txt` on Bookworm):
   ```
   dtparam=i2c_arm=on
   dtoverlay=pwm
   ```
   Reboot.
2. Ensure the `gardyn` user is in the `gpio`, `i2c`, and `video` groups.

### Install
1. Download `gardynd-armv6` from the latest release (or `make build-pi`) and
   copy it to `/usr/local/bin/gardynd`.
2. Copy `config.example.yaml` to `/etc/gardynd/config.yaml` and edit it
   (`identifier`, schedules, optional water threshold).
3. Install the service:
   ```
   sudo cp services/etc/systemd/system/gardynd.service /etc/systemd/system/
   sudo systemctl daemon-reload
   sudo systemctl enable --now gardynd
   ```
4. Verify: `journalctl -u gardynd -f` and `curl localhost:5000/state`. Then add
   the gardynd integration in Home Assistant (auto-discovered via zeroconf, or
   enter the Pi's host/port).

### Side-by-side bring-up (recommended before cutover)
Run gardynd on an alternate HTTP port while the Python service still runs:
```
gardynd --hw=real --config /etc/gardynd/config.yaml --http-port 5001
```
Exercise control + `curl localhost:5001/state`, then switch over by stopping the
old `mqtt.service` and running gardynd on the default port.

### Config / env compatibility
Existing `.env` variables (`MQTT_IDENTIFIER`, `WATER_LOW_CM`, `SENSOR_TYPE`,
camera devices, …) still override the YAML during migration.
```

- [ ] **Step 2: Add a deprecation pointer for the Python service**

Add one line near the existing Python instructions:
```markdown
> **Deprecated:** the Python `mqtt.py`/Flask service is superseded by gardynd
> (see "Running gardynd"). It will be removed once parity is confirmed.
```

- [ ] **Step 3: Verify Markdown renders**

Run: `python3 -c "open('README.md').read()" && echo OK` (sanity that the file is
intact); visually confirm the new section.
Expected: `OK`.

---

### Task 5: Automations deprecation note

**Depends on:** none

**Files:**
- Create: `automations/README.md`

- [ ] **Step 1: Write the note**

`automations/README.md`:
```markdown
# Home Assistant automations (legacy)

These `lights.yml` / `pump.yml` automations drove the Gardyn schedule from Home
Assistant. gardynd now runs the schedule **on-device** (see
`config.example.yaml` `schedules:` and the `Light Schedule` / `Pump Schedule`
switches in Home Assistant).

Keep these only if you prefer HA-driven scheduling; if so, disable the on-device
schedule by setting `schedules.light.enabled: false` (and `pump`) or toggling
the schedule switches off in Home Assistant. A dedicated HA integration for
editing the on-device schedule is planned (separate project).
```

---

### Task 6: Remove the Python service (gated on parity)

**Depends on:** Tasks 1–5 **and** a passing on-device parity check

> **GATE — do not perform Task 6 until every box is checked on the actual Pi:**
> - [ ] gardynd running via systemd for ≥24h with `Restart=always`, no crash loops
> - [ ] Light + pump controllable from the HA integration and `bin/` scripts (REST)
> - [ ] Scheduler drives light/pump at the configured times; survives a reboot
>       (restart catch-up verified)
> - [ ] Water-low interlock blocks the pump from **every** path (REST, button, scheduler)
> - [ ] Temperature, humidity, PCB temp, water level, pump power all present in `/state`
> - [ ] Cameras return frames via `/camera/upper.jpg` and `/camera/lower.jpg`
> - [ ] Over-temp reflected in `/state` (`overtemp`) from the alert pin
> - [ ] Old `mqtt.service` stopped and disabled

- [ ] **Step 1: Stop and disable the old unit on the Pi (manual)**

Run (on the Pi): `sudo systemctl disable --now mqtt.service`

- [ ] **Step 2: Remove Python sources and the old unit from the repo**

Run:
```
git rm -r app/ tests/test_*.py mqtt.py run.py config.py requirements.txt \
  services/etc/systemd/system/mqtt.service
git rm bin/light.sh bin/water.sh bin/get-sensor-data.sh bin/take-pictures.sh
```
(Keep `bin/api-test.sh` if it only curls the REST API — verify; remove if it
shells into Python.)

- [ ] **Step 3: Drop the pigpiod note and Python prerequisites from README**

Edit `README.md`: remove the venv/pip/pigpiod setup steps that only the Python
service needed; remove the "Deprecated" banner from Task 4 Step 2 (the service
it referred to is gone). Leave the hardware wiring/PCB docs intact.

- [ ] **Step 4: Verify the repo still builds and tests pass**

Run: `go build ./... && go test ./... && make build-pi`
Expected: PASS; no references to the removed Python files remain (`grep -rn
mqtt.py .` returns nothing in tracked files).

- [ ] **Step 5: Commit (single commit)**

Run:
```
git add -A
git commit -m "chore: remove Python service after gardynd parity; gardynd is now the controller"
```

---

### Task 7: Finish the branch

**Depends on:** Task 6

- [ ] **Step 1: Run the full suite one final time**

Run: `go test ./... && go vet ./...`
Expected: all PASS.

- [ ] **Step 2: Hand off to branch completion**

Use the `superpowers:finishing-a-development-branch` skill to decide
merge/PR/cleanup for `go-service-rewrite`.

---

## Self-Review

**Spec coverage (migration section):** systemd unit without pigpiod ✓; ARMv6
cross-compile in CI + release artifact ✓; `dtoverlay=pwm` one-time host step
documented ✓; side-by-side bring-up documented ✓; `.env` compatibility noted ✓;
automations kept with deprecation note until the HA-integration project ✓;
Python removal gated behind an explicit on-device parity checklist ✓; ends by
invoking finishing-a-development-branch ✓.

**Placeholder scan:** No code placeholders. The parity GATE is an intentional
human checklist, not deferred implementation. The `bin/api-test.sh` line asks
the executor to verify-then-decide — a concrete instruction, not a TBD.

**Type consistency:** No new Go types introduced except the
`TestExampleConfigLoads` test, which references existing fields
(`Schedules.Light.Enabled`, `Camera.Resolution`) defined in Plans 1–3.

**Dependency audit:** Tasks 1, 2, 3, 5 are independent doc/artifact files (no
shared edits) and can run in parallel; Task 2 adds a test in `internal/config`
but no other task in this plan touches that package, so no conflict. Task 4
(README) is independent of 1–3 in content but logically follows them; it shares
no files. Task 6 edits README again and removes Python — it depends on Tasks 1–5
and the parity gate, and serializes after Task 4 on `README.md`. Task 7 depends
on Task 6. No mis-marked independence.

