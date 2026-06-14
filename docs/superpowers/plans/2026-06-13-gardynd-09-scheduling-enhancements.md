# gardynd Plan 9 — Scheduling Enhancements (richer schedules + DST-correct time) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Layer optional richer-schedule capabilities — per-weekday entries, sunrise/sunset (solar) entries, and light fade/ramp — onto the existing schedule model, and migrate scheduler evaluation from raw minute-of-day ints to a date+location-aware, DST-correct `time.Time` model, while keeping every existing `at:`-only schedule behaving byte-for-byte identically.

**Architecture:** All schedule resolution stays pure in `internal/config`: new optional fields (`Days`, `Solar`, `SolarOffsetMin`, `FadeMin`) on `ScheduleEntry`, a dependency-free NOAA sunrise/sunset calculator in `internal/config/solar.go` exposing a `SunCalc` interface, and new time-aware methods `StateAtTime`/`DueBetweenTimes` that compute each entry's effective minute-of-day for a given local date then reuse the existing most-recent-entry / due-window logic per local day. The scheduler in `internal/core` switches to the time-based API and tracks the previous fire *instant* (`time.Time`) instead of a raw minute so a DST jump neither double-fires nor skips. Light fade stays on the single-writer core goroutine: a faded light-on computes a pure list of brightness steps and drives them via a cancellable stepper that submits intermediate `ActionSetLevel` commands; any new light/pump command cancels an in-progress fade. No external dependencies.

**Tech Stack:** Go stdlib `time` (with `*time.Location` / `time.LoadLocation`), `math` for the solar algorithm, `gopkg.in/yaml.v3` for config; existing single-writer `core`, mutex-guarded `state.Store`, `mock` hw fakes for tests.

**Spec:** `docs/superpowers/specs/2026-06-12-gardynd-go-service-design.md`
**Depends on:** Plan 1 (core, state store, httpapi, config), Plan 2 (sensor interfaces, `sensorMux`), Plan 3 (scheduler, `Schedule`/`ScheduleEntry`/`Schedules`, `StateAt`/`DueBetween`/`Validate`, `NewScheduler`, `Command`/`Target`/`Action`, `mock` hw, `HandlerFull`/`ControlDeps`).

**Cross-branch follow-up (NOT in this plan):** The Home Assistant `set_schedule` service schema and the gardyn HA client (`custom_components/`, branch `ha-integration`) should mirror the new optional fields (`days`, `solar`, `solar_offset_min`, `fade_min`) and the new `location` config. Those files are **not** edited here; this is a deferred follow-up on the HA branch.

---

## File Structure (this plan)

```
internal/config/schedule.go        MODIFY: new optional fields on ScheduleEntry; Validate extensions;
                                   effective-minute resolution; StateAtTime/DueBetweenTimes (time-aware);
                                   keep StateAt/DueBetween (back-compat, used by old tests)
internal/config/schedule_test.go   MODIFY: append weekday + back-compat field tests (keep existing green)
internal/config/solar.go           CREATE: SunCalc interface + dependency-free NOAA sunrise/sunset calculator
internal/config/solar_test.go      CREATE: calculator vs published values (tolerance) + edge behavior
internal/config/timeeval_test.go   CREATE: StateAtTime/DueBetweenTimes incl. weekday, solar, DST-boundary
internal/config/config.go          MODIFY: LocationConfig field + defaults + LAT/LON/TZ env hooks;
                                   Zone() *time.Location resolver
internal/config/config_test.go     MODIFY: append location default/env/resolver tests
internal/core/fade.go              CREATE: pure fadeSteps(from, to, fadeMin) []fadeStep
internal/core/fade_test.go         CREATE: fadeSteps pure-function tests
internal/core/core.go              MODIFY: cancellable fade execution on the core goroutine;
                                   cancel-on-any-command; ApplyLightFade entrypoint
internal/core/core_test.go         MODIFY: append faded-on + fade-cancellation tests (-race)
internal/core/scheduler.go         MODIFY: time-based API; track previous fire instant; pass loc + SunCalc;
                                   trigger fade on faded light-on entries
internal/core/scheduler_test.go    MODIFY: migrate to time-based API; catch-up + fade-trigger tests
cmd/gardynd/main.go                MODIFY: load location + SunCalc, pass to scheduler
```

---

### Task 1: `ScheduleEntry` new optional fields + `Validate` extensions (back-compat)

**Depends on:** none (within this plan; touches only `internal/config/schedule.go` + `schedule_test.go`, additively)

**Files:**
- Modify: `internal/config/schedule.go`
- Modify: `internal/config/schedule_test.go`

Add four optional fields. They are all `omitempty`, so an existing `{at, action, brightness}` entry marshals/unmarshals and validates exactly as before. `Validate` gains: unknown weekday tokens rejected, `Solar` other than `sunrise`/`sunset` rejected, negative `FadeMin`/`SolarOffsetMin` rejected. The existing requirement that `At` parse as `HH:MM` is **relaxed only when `Solar` is set** (a solar entry ignores `At`).

- [ ] **Step 1: Write the failing tests (append to `internal/config/schedule_test.go`)**

```go
func TestValidateDaysTokens(t *testing.T) {
	good := Schedule{Entries: []ScheduleEntry{
		{At: "06:00", Action: "on", Brightness: 70, Days: []string{"Mon", "tue", "SUN"}},
	}}
	if err := good.Validate(); err != nil {
		t.Errorf("good days: unexpected error %v", err)
	}
	bad := Schedule{Entries: []ScheduleEntry{
		{At: "06:00", Action: "on", Days: []string{"funday"}},
	}}
	if err := bad.Validate(); err == nil {
		t.Error("bad day token \"funday\": expected error, got nil")
	}
}

func TestValidateSolarFields(t *testing.T) {
	good := Schedule{Entries: []ScheduleEntry{
		{Solar: "sunrise", SolarOffsetMin: -30, Action: "on", Brightness: 60},
		{Solar: "sunset", SolarOffsetMin: 15, Action: "off"},
	}}
	if err := good.Validate(); err != nil {
		t.Errorf("good solar: unexpected error %v", err)
	}
	bad := Schedule{Entries: []ScheduleEntry{
		{Solar: "noon", Action: "on"},
	}}
	if err := bad.Validate(); err == nil {
		t.Error("bad solar \"noon\": expected error, got nil")
	}
}

func TestValidateNegativeFade(t *testing.T) {
	bad := Schedule{Entries: []ScheduleEntry{
		{At: "06:00", Action: "on", FadeMin: -5},
	}}
	if err := bad.Validate(); err == nil {
		t.Error("negative fade: expected error, got nil")
	}
}

func TestValidateBackCompatPlainEntry(t *testing.T) {
	// A plain at-only entry must still validate exactly as before.
	plain := Schedule{Entries: []ScheduleEntry{{At: "06:00", Action: "on", Brightness: 70}}}
	if err := plain.Validate(); err != nil {
		t.Errorf("plain entry: unexpected error %v", err)
	}
	// A solar entry needs no valid At.
	solar := Schedule{Entries: []ScheduleEntry{{Solar: "sunrise", Action: "on"}}}
	if err := solar.Validate(); err != nil {
		t.Errorf("solar entry without At: unexpected error %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config/ -run 'TestValidateDaysTokens|TestValidateSolarFields|TestValidateNegativeFade|TestValidateBackCompatPlainEntry' -v`
Expected: build failure — `unknown field Days in struct literal` (and `Solar`, `SolarOffsetMin`, `FadeMin`).

- [ ] **Step 3: Add the fields and extend `Validate`**

In `internal/config/schedule.go`, replace the `ScheduleEntry` struct:
```go
type ScheduleEntry struct {
	At             string   `yaml:"at" json:"at"`                                           // "HH:MM" (ignored when Solar is set)
	Action         string   `yaml:"action" json:"action"`                                   // "on" | "off"
	Brightness     int      `yaml:"brightness,omitempty" json:"brightness,omitempty"`       // light only
	Days           []string `yaml:"days,omitempty" json:"days,omitempty"`                   // "mon".."sun"; empty = every day
	Solar          string   `yaml:"solar,omitempty" json:"solar,omitempty"`                 // "sunrise" | "sunset"; overrides At
	SolarOffsetMin int      `yaml:"solar_offset_min,omitempty" json:"solar_offset_min,omitempty"`
	FadeMin        int      `yaml:"fade_min,omitempty" json:"fade_min,omitempty"`           // light "on" ramp duration
}
```

Add the weekday parser and `HasSolar` helper near the top of the file (below the structs, above `minutes`):
```go
// weekdayTokens maps lowercase 3-letter day tokens to time.Weekday.
var weekdayTokens = map[string]time.Weekday{
	"sun": time.Sunday, "mon": time.Monday, "tue": time.Tuesday,
	"wed": time.Wednesday, "thu": time.Thursday, "fri": time.Friday, "sat": time.Saturday,
}

// parseDay returns the time.Weekday for a case-insensitive token, or ok=false.
func parseDay(tok string) (time.Weekday, bool) {
	wd, ok := weekdayTokens[strings.ToLower(strings.TrimSpace(tok))]
	return wd, ok
}

// HasSolar reports whether the entry resolves its time from a solar event.
func (e ScheduleEntry) HasSolar() bool { return e.Solar != "" }
```

Add `"time"` to the import block.

Replace `Validate` with the extended version:
```go
// Validate checks that every entry is well-formed:
//   - solar entries: Solar must be "sunrise" or "sunset" (At is ignored);
//   - non-solar entries: At must parse as HH:MM;
//   - action must be "on" or "off";
//   - any Days tokens must be known 3-letter day names (case-insensitive);
//   - FadeMin and SolarOffsetMin handled below.
// Plain {at, action, brightness} entries validate exactly as in Plan 3.
func (s Schedule) Validate() error {
	for _, e := range s.Entries {
		if e.HasSolar() {
			if e.Solar != "sunrise" && e.Solar != "sunset" {
				return fmt.Errorf("entry solar %q: must be sunrise|sunset", e.Solar)
			}
		} else if _, err := minutes(e.At); err != nil {
			return fmt.Errorf("entry at %q: %w", e.At, err)
		}
		if e.Action != "on" && e.Action != "off" {
			return fmt.Errorf("entry at %q: action must be on|off", e.At)
		}
		for _, d := range e.Days {
			if _, ok := parseDay(d); !ok {
				return fmt.Errorf("entry at %q: unknown day %q", e.At, d)
			}
		}
		if e.FadeMin < 0 {
			return fmt.Errorf("entry at %q: fade_min must be >= 0", e.At)
		}
	}
	return nil
}
```

> Note: `SolarOffsetMin` may be negative (sunrise minus 30 min is valid), so it is intentionally **not** bounded below. Only `FadeMin` is non-negative-checked. The location-required-when-solar check lives in config validation (Task 3), not here, because `Schedule` has no access to `LocationConfig`.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/config/ -run 'TestValidate' -v`
Expected: PASS for `TestValidate`, `TestValidateDaysTokens`, `TestValidateSolarFields`, `TestValidateNegativeFade`, `TestValidateBackCompatPlainEntry` (the original `TestValidate` still passes).

- [ ] **Step 5: Run full config suite to confirm no regressions**

Run: `go test ./internal/config/ -v`
Expected: all existing tests (`TestStateAtMidTimeline`, `TestMidnightWrap`, `TestDueEntries`, `TestDueBetweenMidnightCrossing`, etc.) PASS plus the new ones. The minute-based `StateAt`/`DueBetween` are untouched in this task.

---

### Task 2: `solar.go` — NOAA sunrise/sunset calculator + `SunCalc` interface

**Depends on:** none (within this plan; new files only)

**Files:**
- Create: `internal/config/solar.go`
- Create: `internal/config/solar_test.go`

A pure, dependency-free implementation of the standard NOAA solar-position algorithm. `SunCalc` is the seam the time-aware evaluation (Task 4) and the scheduler (Task 5) depend on. `Sunrise`/`Sunset` return the event as a UTC instant; callers convert to local time via the schedule's `*time.Location`.

- [ ] **Step 1: Write the failing tests**

`internal/config/solar_test.go`:
```go
package config

import (
	"testing"
	"time"
)

// withinMinutes asserts got is within tol minutes of want.
func withinMinutes(t *testing.T, label string, got, want time.Time, tol float64) {
	t.Helper()
	diff := got.Sub(want).Minutes()
	if diff < 0 {
		diff = -diff
	}
	if diff > tol {
		t.Errorf("%s: got %s, want ~%s (off by %.1f min, tol %.0f)",
			label, got.UTC().Format("15:04"), want.UTC().Format("15:04"), diff, tol)
	}
}

// New York City, 2026-06-21 (summer solstice). Published NOAA values (UTC):
// sunrise ~09:25 UTC (05:25 EDT), sunset ~00:31 UTC next day (20:31 EDT).
// We assert within 4 minutes — algorithmic vs. published rounding.
func TestSolarNYCSolstice(t *testing.T) {
	var sc SunCalc = NOAASun{}
	lat, lon := 40.7128, -74.0060
	date := time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)

	wantRise := time.Date(2026, 6, 21, 9, 25, 0, 0, time.UTC)
	wantSet := time.Date(2026, 6, 22, 0, 31, 0, 0, time.UTC)

	withinMinutes(t, "NYC sunrise", sc.Sunrise(date, lat, lon), wantRise, 4)
	withinMinutes(t, "NYC sunset", sc.Sunset(date, lat, lon), wantSet, 4)
}

// London, 2026-03-20 (near equinox). Published (UTC): sunrise ~06:02, sunset ~18:14.
func TestSolarLondonEquinox(t *testing.T) {
	var sc SunCalc = NOAASun{}
	lat, lon := 51.5074, -0.1278
	date := time.Date(2026, 3, 20, 12, 0, 0, 0, time.UTC)

	wantRise := time.Date(2026, 3, 20, 6, 2, 0, 0, time.UTC)
	wantSet := time.Date(2026, 3, 20, 18, 14, 0, 0, time.UTC)

	withinMinutes(t, "London sunrise", sc.Sunrise(date, lat, lon), wantRise, 5)
	withinMinutes(t, "London sunset", sc.Sunset(date, lat, lon), wantSet, 5)
}

// Sunrise must be before sunset on a normal day, and both on the requested date.
func TestSolarOrderingAndDate(t *testing.T) {
	var sc SunCalc = NOAASun{}
	date := time.Date(2026, 6, 21, 0, 0, 0, 0, time.UTC)
	rise := sc.Sunrise(date, 40.7128, -74.0060)
	set := sc.Sunset(date, 40.7128, -74.0060)
	if !rise.Before(set) {
		t.Errorf("sunrise %s not before sunset %s", rise, set)
	}
	if rise.UTC().Year() != 2026 || rise.UTC().YearDay() != date.UTC().YearDay() {
		t.Errorf("sunrise %s not on requested UTC date", rise)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config/ -run 'TestSolar' -v`
Expected: build failure — `undefined: SunCalc` / `undefined: NOAASun`.

- [ ] **Step 3: Implement the calculator**

`internal/config/solar.go`:
```go
package config

import (
	"math"
	"time"
)

// SunCalc resolves solar events for a date at a latitude/longitude. The returned
// instants are UTC; callers convert to local time via the schedule's location.
// Plain "at:" schedule entries never call this.
type SunCalc interface {
	Sunrise(date time.Time, lat, lon float64) time.Time
	Sunset(date time.Time, lat, lon float64) time.Time
}

// NOAASun implements SunCalc using the standard NOAA solar-position algorithm.
// It is pure (no I/O, no globals) and dependency-free.
type NOAASun struct{}

var _ SunCalc = NOAASun{}

const (
	deg2rad = math.Pi / 180
	rad2deg = 180 / math.Pi
	// Sun's geometric zenith at sunrise/sunset (90.833°: 0.833° refraction+radius).
	zenithDeg = 90.833
)

func (NOAASun) Sunrise(date time.Time, lat, lon float64) time.Time {
	return solarEvent(date, lat, lon, true)
}
func (NOAASun) Sunset(date time.Time, lat, lon float64) time.Time {
	return solarEvent(date, lat, lon, false)
}

// solarEvent computes the UTC instant of sunrise (rise=true) or sunset
// (rise=false) for the UTC calendar date of `date` at lat/lon.
// Implements the NOAA equations (Jean Meeus, Astronomical Algorithms).
func solarEvent(date time.Time, lat, lon float64, rise bool) time.Time {
	d := date.UTC()
	y, m, day := d.Date()

	// Julian day for 00:00 UTC of the calendar date.
	jd := julianDay(y, int(m), day)
	// Julian century from J2000.0, evaluated near solar noon (jd + 0.5).
	jc := (jd + 0.5 - 2451545.0) / 36525.0

	// Geometric mean longitude of the sun (deg).
	gml := math.Mod(280.46646+jc*(36000.76983+jc*0.0003032), 360)
	if gml < 0 {
		gml += 360
	}
	// Geometric mean anomaly (deg).
	gma := 357.52911 + jc*(35999.05029-0.0001537*jc)
	// Eccentricity of earth's orbit.
	ecc := 0.016708634 - jc*(0.000042037+0.0000001267*jc)
	// Sun's equation of center.
	gmaR := gma * deg2rad
	ctr := math.Sin(gmaR)*(1.914602-jc*(0.004817+0.000014*jc)) +
		math.Sin(2*gmaR)*(0.019993-0.000101*jc) +
		math.Sin(3*gmaR)*0.000289
	trueLong := gml + ctr
	// Apparent longitude (deg).
	appLong := trueLong - 0.00569 - 0.00478*math.Sin((125.04-1934.136*jc)*deg2rad)
	// Mean obliquity of the ecliptic (deg) + correction.
	meanObliq := 23 + (26+(21.448-jc*(46.815+jc*(0.00059-jc*0.001813)))/60)/60
	obliq := meanObliq + 0.00256*math.Cos((125.04-1934.136*jc)*deg2rad)
	// Sun declination (deg).
	declR := math.Asin(math.Sin(obliq*deg2rad) * math.Sin(appLong*deg2rad))

	// Equation of time (minutes).
	vary := math.Tan(obliq/2*deg2rad) * math.Tan(obliq/2*deg2rad)
	gmlR := gml * deg2rad
	eqTime := 4 * rad2deg * (vary*math.Sin(2*gmlR) -
		2*ecc*math.Sin(gmaR) +
		4*ecc*vary*math.Sin(gmaR)*math.Cos(2*gmlR) -
		0.5*vary*vary*math.Sin(4*gmlR) -
		1.25*ecc*ecc*math.Sin(2*gmaR))

	// Hour angle (deg) for the configured zenith.
	latR := lat * deg2rad
	cosH := (math.Cos(zenithDeg*deg2rad) - math.Sin(latR)*math.Sin(declR)) /
		(math.Cos(latR) * math.Cos(declR))
	// Clamp for polar day/night so we still return a deterministic instant.
	if cosH > 1 {
		cosH = 1
	} else if cosH < -1 {
		cosH = -1
	}
	haDeg := math.Acos(cosH) * rad2deg

	// Solar noon (minutes after UTC midnight) at this longitude.
	noonMin := 720 - 4*lon - eqTime
	var eventMin float64
	if rise {
		eventMin = noonMin - 4*haDeg
	} else {
		eventMin = noonMin + 4*haDeg
	}

	// Build the UTC instant on the same calendar date, then add minutes.
	base := time.Date(y, m, day, 0, 0, 0, 0, time.UTC)
	return base.Add(time.Duration(eventMin * float64(time.Minute)))
}

// julianDay returns the Julian Day Number for 00:00 UTC of the given Gregorian date.
func julianDay(y, m, d int) float64 {
	if m <= 2 {
		y--
		m += 12
	}
	a := y / 100
	b := 2 - a + a/4
	return math.Floor(365.25*float64(y+4716)) +
		math.Floor(30.6001*float64(m+1)) +
		float64(d) + float64(b) - 1524.5
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/config/ -run 'TestSolar' -v`
Expected:
```
--- PASS: TestSolarNYCSolstice (0.00s)
--- PASS: TestSolarLondonEquinox (0.00s)
--- PASS: TestSolarOrderingAndDate (0.00s)
PASS
```

> Tolerance rationale: the NOAA algorithm and published almanac times can differ by a couple of minutes due to refraction-model and rounding choices; 4–5 min is the conventional tolerance for this algorithm and keeps the test robust without being meaningless.

---

### Task 3: Config — `LocationConfig` + env wiring + `Zone()` resolver

**Depends on:** none (within this plan; touches only `internal/config/config.go` + `config_test.go`)

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`

Adds optional location. The resolver is named `Zone()` (not `Location()`) because `Location` is already the struct field name and Go forbids a field and method sharing a name. `Zone()` returns the `*time.Location` for the configured timezone (falling back to `time.Local` when unset/invalid), which the scheduler uses for DST-correct evaluation. `LAT`/`LON`/`TZ` env vars are optional overrides.

- [ ] **Step 1: Write the failing tests (append to `internal/config/config_test.go`)**

```go
func TestLocationDefaultsEmpty(t *testing.T) {
	c, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if c.Location.Latitude != 0 || c.Location.Longitude != 0 || c.Location.TimeZone != "" {
		t.Errorf("location defaults = %+v, want zero-value", c.Location)
	}
	// With no timezone, Zone() falls back to Local (never nil).
	if c.Zone() == nil {
		t.Error("Zone() returned nil")
	}
}

func TestLocationEnvOverride(t *testing.T) {
	t.Setenv("LAT", "40.7128")
	t.Setenv("LON", "-74.0060")
	t.Setenv("TZ", "America/New_York")
	c, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if c.Location.Latitude != 40.7128 || c.Location.Longitude != -74.0060 {
		t.Errorf("lat/lon = %v/%v, want 40.7128/-74.0060", c.Location.Latitude, c.Location.Longitude)
	}
	if c.Location.TimeZone != "America/New_York" {
		t.Errorf("tz = %q, want America/New_York", c.Location.TimeZone)
	}
	loc := c.Zone()
	if loc == nil || loc.String() != "America/New_York" {
		t.Errorf("Zone() = %v, want America/New_York", loc)
	}
}

func TestLocationInvalidTZFallsBack(t *testing.T) {
	t.Setenv("TZ", "Not/AZone")
	c, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	// Invalid zone must not panic and must not be nil.
	if c.Zone() == nil {
		t.Error("Zone() returned nil for invalid tz")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config/ -run 'TestLocation' -v`
Expected: build failure — `c.Location undefined` (the `Location` field and the `Zone()` method are both added in Step 3).

- [ ] **Step 3: Add the config type, field, defaults note, env hooks, and resolver**

In `internal/config/config.go`, add `"time"` to the imports, then add the type (near the other config structs):
```go
type LocationConfig struct {
	Latitude  float64 `yaml:"latitude"`
	Longitude float64 `yaml:"longitude"`
	TimeZone  string  `yaml:"timezone"` // IANA name, e.g. "America/New_York"
}
```

Add the field to `Config`:
```go
type Config struct {
	HTTP       HTTPConfig     `yaml:"http"`
	Device     DeviceConfig   `yaml:"device"`
	Camera     CameraConfig   `yaml:"camera"`
	SensorType string         `yaml:"sensor_type"`
	Schedules  Schedules      `yaml:"schedules"`
	Water      WaterConfig    `yaml:"water"`
	OverTemp   OverTempConfig `yaml:"overtemp"`
	Location   LocationConfig `yaml:"location"`
}
```

`defaults()` needs no change — the zero `LocationConfig` (empty TZ → `time.Local`) is the correct default; do not invent coordinates.

In `applyEnv`, append the location hooks:
```go
	envFloat(&c.Location.Latitude, "LAT")
	envFloat(&c.Location.Longitude, "LON")
	envStr(&c.Location.TimeZone, "TZ")
```

Add the resolver method (after `applyEnv`):
```go
// Zone returns the *time.Location for the configured IANA timezone. When unset
// or invalid it falls back to time.Local; it never returns nil. The scheduler
// uses this for DST-correct evaluation.
func (c Config) Zone() *time.Location {
	if c.Location.TimeZone == "" {
		return time.Local
	}
	loc, err := time.LoadLocation(c.Location.TimeZone)
	if err != nil {
		return time.Local
	}
	return loc
}

// HasSolarEntry reports whether any schedule entry resolves from a solar event.
func (c Config) HasSolarEntry() bool {
	for _, s := range []Schedule{c.Schedules.Light, c.Schedules.Pump} {
		for _, e := range s.Entries {
			if e.HasSolar() {
				return true
			}
		}
	}
	return false
}
```

> Solar-requires-location rule (Decision 3): rather than failing `Schedule.Validate` (which has no location context), `main.go` (Task 6) logs a warning if `HasSolarEntry()` is true but `Location.Latitude == 0 && Location.Longitude == 0`. The solar calculator still returns a deterministic instant at lat/lon (0,0), so behavior is defined, not a crash. This keeps `Schedule.Validate` pure and location-free.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/config/ -run 'TestLocation' -v`
Expected:
```
--- PASS: TestLocationDefaultsEmpty (0.00s)
--- PASS: TestLocationEnvOverride (0.00s)
--- PASS: TestLocationInvalidTZFallsBack (0.00s)
PASS
```

- [ ] **Step 5: Run full config suite to confirm no regressions**

Run: `go test ./internal/config/ -v`
Expected: all existing config + schedule + solar tests PASS plus the three new location tests.

---

### Task 4: Time-aware `StateAtTime` / `DueBetweenTimes` (weekday + solar resolution)

**Depends on:** Task 1 (new fields + `parseDay`/`HasSolar`), Task 2 (`SunCalc`), Task 3 (`Zone()` for tests)

**Files:**
- Modify: `internal/config/schedule.go`
- Create: `internal/config/timeeval_test.go`

Introduce the date+location-aware evaluation. For a given local date these compute each entry's *effective minute-of-day* (plain entries: `minutes(At)`; solar entries: solar-event local minute + `SolarOffsetMin`), filter by weekday, then reuse the existing most-recent-entry (`StateAt`) and due-window (`DueBetween`) logic — operating on the resolved minutes for that local day. Tracking the previous *instant* and crossing day boundaries correctly is the scheduler's job (Task 5); these methods evaluate a single now-instant / a single (prev,now) window.

The old minute-based `StateAt`/`DueBetween` are **kept** (Plan 3 tests still use them and they remain the building block via `effectiveEntries`). No existing test is migrated or deleted.

- [ ] **Step 1: Write the failing tests**

`internal/config/timeeval_test.go`:
```go
package config

import (
	"testing"
	"time"
)

func nyc(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skipf("tzdata unavailable: %v", err)
	}
	return loc
}

// A Mon-only "on" entry is active Monday, inactive Tuesday (Decision 2).
func TestStateAtTimeWeekday(t *testing.T) {
	loc := nyc(t)
	s := Schedule{Enabled: true, Entries: []ScheduleEntry{
		{At: "06:00", Action: "on", Brightness: 70, Days: []string{"mon"}},
		{At: "08:00", Action: "off", Days: []string{"mon"}},
	}}
	// 2026-06-15 is a Monday; 2026-06-16 a Tuesday.
	mon := time.Date(2026, 6, 15, 7, 0, 0, 0, loc)
	tue := time.Date(2026, 6, 16, 7, 0, 0, 0, loc)

	st, ok := s.StateAtTime(mon, loc, NOAASun{})
	if !ok || !st.On || st.Brightness != 70 {
		t.Errorf("Monday 07:00 => %+v ok=%v, want on@70", st, ok)
	}
	st, ok = s.StateAtTime(tue, loc, NOAASun{})
	if ok && st.On {
		t.Errorf("Tuesday 07:00 => %+v, want not-on (entry excluded)", st)
	}
}

// Back-compat: a plain at-only schedule evaluated via StateAtTime matches StateAt.
func TestStateAtTimeBackCompat(t *testing.T) {
	loc := nyc(t)
	s := lightSched() // from schedule_test.go: 06:00 on@70, 09:00 off, 17:00 on@50, 20:00 off
	now := time.Date(2026, 6, 15, 18, 0, 0, 0, loc)
	st, ok := s.StateAtTime(now, loc, NOAASun{})
	if !ok || !st.On || st.Brightness != 50 {
		t.Errorf("18:00 => %+v ok=%v, want on@50 (same as StateAt)", st, ok)
	}
}

// Solar entry: an "on" 30 min before sunrise is on after that time, off before.
func TestStateAtTimeSolar(t *testing.T) {
	loc := nyc(t)
	lat, lon := 40.7128, -74.0060
	date := time.Date(2026, 6, 21, 0, 0, 0, 0, time.UTC)
	rise := (NOAASun{}).Sunrise(date, lat, lon).In(loc)
	s := Schedule{Enabled: true, Entries: []ScheduleEntry{
		{Solar: "sunrise", SolarOffsetMin: -30, Action: "on", Brightness: 80},
		{At: "23:59", Action: "off"},
	}}
	// Provide a SunCalc that returns our known instant regardless of args.
	sun := fixedSun{rise: (NOAASun{}).Sunrise(date, lat, lon)}

	after := rise.Add(5 * time.Minute) // 25 min before sunrise => entry already fired
	st, ok := s.StateAtTime(time.Date(rise.Year(), rise.Month(), rise.Day(),
		after.Hour(), after.Minute(), 0, 0, loc), loc, sun)
	if !ok || !st.On || st.Brightness != 80 {
		t.Errorf("after solar-on => %+v ok=%v, want on@80", st, ok)
	}
}

type fixedSun struct{ rise, set time.Time }

func (f fixedSun) Sunrise(time.Time, float64, float64) time.Time { return f.rise }
func (f fixedSun) Sunset(time.Time, float64, float64) time.Time  { return f.set }

// DueBetweenTimes returns the single on-entry crossed in a one-minute window.
func TestDueBetweenTimesBasic(t *testing.T) {
	loc := nyc(t)
	s := Schedule{Enabled: true, Entries: []ScheduleEntry{
		{At: "06:00", Action: "on", Brightness: 70},
		{At: "20:00", Action: "off"},
	}}
	prev := time.Date(2026, 6, 15, 5, 59, 30, 0, loc)
	now := time.Date(2026, 6, 15, 6, 0, 30, 0, loc)
	due := s.DueBetweenTimes(prev, now, loc, NOAASun{})
	if len(due) != 1 || due[0].Action != "on" {
		t.Errorf("due = %+v, want [on@06:00]", due)
	}
}

// DST spring-forward: on 2026-03-08 America/New_York skips 02:00→03:00.
// A 02:30 entry does not exist; an entry at 03:30 must still fire exactly once
// in a window straddling the gap, and a window must not double-report.
func TestDueBetweenTimesDSTSpringForward(t *testing.T) {
	loc := nyc(t)
	s := Schedule{Enabled: true, Entries: []ScheduleEntry{
		{At: "03:30", Action: "on", Brightness: 60},
	}}
	// Window from 01:59 EST to 03:31 EDT spans the spring-forward gap.
	prev := time.Date(2026, 3, 8, 1, 59, 0, 0, loc)
	now := time.Date(2026, 3, 8, 3, 31, 0, 0, loc)
	due := s.DueBetweenTimes(prev, now, loc, NOAASun{})
	if len(due) != 1 || due[0].At != "03:30" {
		t.Errorf("spring-forward due = %+v, want exactly [03:30 on]", due)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config/ -run 'StateAtTime|DueBetweenTimes' -v`
Expected: build failure — `s.StateAtTime undefined` / `s.DueBetweenTimes undefined`.

- [ ] **Step 3: Implement the time-aware methods**

Append to `internal/config/schedule.go`:
```go
// effectiveEntry pairs an entry with its resolved minute-of-day for one local date.
type effectiveEntry struct {
	min   int
	entry ScheduleEntry
}

// localMidnight returns 00:00 of t's local date in loc.
func localMidnight(t time.Time, loc *time.Location) time.Time {
	y, m, d := t.In(loc).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, loc)
}

// minutesInLoc returns the minute-of-day of instant t within loc (0..1439),
// honoring whatever wall-clock offset (incl. DST) applies at t.
func minutesInLoc(t time.Time, loc *time.Location) int {
	lt := t.In(loc)
	return lt.Hour()*60 + lt.Minute()
}

// effectiveEntries resolves all entries to their minute-of-day for the LOCAL
// date of `date`, filtering out entries whose Days set excludes that weekday.
// Plain entries resolve from At; solar entries from sun + SolarOffsetMin.
// lat/lon are taken implicitly via the caller (sun closure already bound);
// here we pass them through SunCalc which receives the date only — see note.
func (s Schedule) effectiveEntries(date time.Time, loc *time.Location, sun SunCalc, lat, lon float64) []effectiveEntry {
	wd := date.In(loc).Weekday()
	midnight := localMidnight(date, loc)
	var out []effectiveEntry
	for _, e := range s.Entries {
		if !dayMatches(e, wd) {
			continue
		}
		var min int
		if e.HasSolar() {
			var ev time.Time
			if e.Solar == "sunset" {
				ev = sun.Sunset(midnight, lat, lon)
			} else {
				ev = sun.Sunrise(midnight, lat, lon)
			}
			min = minutesInLoc(ev, loc) + e.SolarOffsetMin
		} else {
			m, err := minutes(e.At)
			if err != nil {
				continue
			}
			min = m
		}
		// Clamp into the day so wrap logic stays well-defined.
		for min < 0 {
			min += 1440
		}
		min %= 1440
		out = append(out, effectiveEntry{min: min, entry: e})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].min < out[j].min })
	return out
}

// dayMatches reports whether an entry applies on weekday wd (empty Days = every day).
func dayMatches(e ScheduleEntry, wd time.Weekday) bool {
	if len(e.Days) == 0 {
		return true
	}
	for _, d := range e.Days {
		if got, ok := parseDay(d); ok && got == wd {
			return true
		}
	}
	return false
}
```

Because `SunCalc.Sunrise/Sunset` take `lat, lon` but `Schedule` does not hold them, expose lat/lon through the public methods. Add the public API:
```go
// SchedLocation bundles the location parameters time-aware evaluation needs.
// (Latitude/Longitude feed SunCalc; loc handles wall-clock + DST.)
type SchedLocation struct {
	Loc *time.Location
	Lat float64
	Lon float64
}

// StateAtTime returns the channel state implied at the instant `now`, evaluated
// against the LOCAL date in loc, including weekday filtering and solar resolution.
// For plain at-only schedules the result matches StateAt(minutesInLoc(now)).
func (s Schedule) StateAtTime(now time.Time, loc *time.Location, sun SunCalc) (ChannelState, bool) {
	return s.stateAt(now, SchedLocation{Loc: loc}, sun)
}

// StateAtLoc is StateAtTime with explicit lat/lon for solar entries.
func (s Schedule) StateAtLoc(now time.Time, sl SchedLocation, sun SunCalc) (ChannelState, bool) {
	return s.stateAt(now, sl, sun)
}

func (s Schedule) stateAt(now time.Time, sl SchedLocation, sun SunCalc) (ChannelState, bool) {
	loc := sl.Loc
	if loc == nil {
		loc = time.Local
	}
	nowMin := minutesInLoc(now, loc)
	es := s.effectiveEntries(now, loc, sun, sl.Lat, sl.Lon)
	if len(es) == 0 {
		// Today has no applicable entries; carry from the most recent prior day
		// that does (look back up to 7 days for weekday-restricted schedules).
		for back := 1; back <= 7; back++ {
			d := now.AddDate(0, 0, -back)
			pes := s.effectiveEntries(d, loc, sun, sl.Lat, sl.Lon)
			if len(pes) > 0 {
				e := pes[len(pes)-1].entry
				return ChannelState{On: e.Action == "on", Brightness: e.Brightness}, true
			}
		}
		return ChannelState{}, false
	}
	idx := -1
	for i, e := range es {
		if e.min <= nowMin {
			idx = i
		}
	}
	if idx == -1 {
		// Before the first entry today: carry the last applicable entry from a
		// prior day (handles weekday gaps and the simple wrap case).
		for back := 1; back <= 7; back++ {
			d := now.AddDate(0, 0, -back)
			pes := s.effectiveEntries(d, loc, sun, sl.Lat, sl.Lon)
			if len(pes) > 0 {
				e := pes[len(pes)-1].entry
				return ChannelState{On: e.Action == "on", Brightness: e.Brightness}, true
			}
		}
		idx = len(es) - 1
	}
	e := es[idx].entry
	return ChannelState{On: e.Action == "on", Brightness: e.Brightness}, true
}

// DueBetweenTimes returns entries whose effective instant falls in (prev, now].
// It resolves entries per local date for every local day the window touches, so
// a window spanning midnight or a DST transition fires each entry exactly once.
func (s Schedule) DueBetweenTimes(prev, now time.Time, loc *time.Location, sun SunCalc) []ScheduleEntry {
	return s.dueBetween(prev, now, SchedLocation{Loc: loc}, sun)
}

// DueBetweenLoc is DueBetweenTimes with explicit lat/lon for solar entries.
func (s Schedule) DueBetweenLoc(prev, now time.Time, sl SchedLocation, sun SunCalc) []ScheduleEntry {
	return s.dueBetween(prev, now, sl, sun)
}

func (s Schedule) dueBetween(prev, now time.Time, sl SchedLocation, sun SunCalc) []ScheduleEntry {
	loc := sl.Loc
	if loc == nil {
		loc = time.Local
	}
	if !now.After(prev) {
		return nil
	}
	var out []ScheduleEntry
	// Iterate each local date the (prev, now] window touches.
	day := localMidnight(prev, loc)
	last := localMidnight(now, loc)
	for !day.After(last) {
		for _, ee := range s.effectiveEntries(day, loc, sun, sl.Lat, sl.Lon) {
			// Build the entry's effective instant on this local date.
			inst := day.Add(time.Duration(ee.min) * time.Minute)
			if inst.After(prev) && !inst.After(now) {
				out = append(out, ee.entry)
			}
		}
		day = day.AddDate(0, 0, 1)
	}
	return out
}
```

Add nothing to imports — `time` and `sort` are already imported (sort from Plan 3, time from Task 1).

> Design tradeoff (DST + due-window): `DueBetweenTimes` builds each entry's instant via `day.Add(min*Minute)` on a `time.Time` already located in `loc`, so Go's library applies the correct offset. During spring-forward the skipped wall-clock hour simply has no instant, so an entry inside the gap is folded to the adjacent valid instant by `time.Date` semantics and still fires exactly once; entries outside the gap are unaffected. During fall-back the duplicated hour is covered once because we iterate instants, not wall-clock labels, and the `(prev, now]` half-open comparison prevents a second match. This is why the scheduler must track the previous *instant* (Task 5), not a minute.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/config/ -run 'StateAtTime|DueBetweenTimes' -v`
Expected:
```
--- PASS: TestStateAtTimeWeekday (0.00s)
--- PASS: TestStateAtTimeBackCompat (0.00s)
--- PASS: TestStateAtTimeSolar (0.00s)
--- PASS: TestDueBetweenTimesBasic (0.00s)
--- PASS: TestDueBetweenTimesDSTSpringForward (0.00s)
PASS
```

- [ ] **Step 5: Run full config suite to confirm no regressions**

Run: `go test ./internal/config/ -v`
Expected: every prior config/schedule/solar/location test still PASS plus the five new time-eval tests. The minute-based `StateAt`/`DueBetween` and their Plan 3 tests are untouched.

---

### Task 5: Refactor `scheduler.go` to the time-based API (track previous instant)

**Depends on:** Task 4 (`StateAtLoc`/`DueBetweenLoc`, `SchedLocation`, `SunCalc`), Task 7 (`ApplyLightFade` for fade trigger — declare dependency; implement fade-trigger wiring here but the method comes from Task 7)

> Ordering note: This task’s fade-trigger step calls `core.ApplyLightFade` (Task 7). Execute Task 7 **before** Task 5’s Step 3 fade wiring, or split: do Steps 1–2 (time-based migration, no fade) first, then return for the fade-trigger step after Task 7. The catch-up/DST behavior in Steps 1–2 does not need Task 7.

**Files:**
- Modify: `internal/core/scheduler.go`
- Modify: `internal/core/scheduler_test.go`

The scheduler now holds a `*time.Location`, a `SunCalc`, and lat/lon, tracks `prevTick time.Time`, and uses `DueBetweenLoc`/`StateAtLoc`. `CatchUpAt(nowMin int)` is **kept** for the existing Plan 3 tests (it forwards to a synthetic local instant); a new `CatchUpAtTime(now time.Time)` is the production path.

- [ ] **Step 1: Migrate the existing scheduler tests + add new ones**

Replace `internal/core/scheduler_test.go` with:
```go
package core

import (
	"testing"
	"time"

	"github.com/iot-root/garden-of-eden/internal/config"
	"github.com/iot-root/garden-of-eden/internal/hw/mock"
	"github.com/iot-root/garden-of-eden/internal/state"
)

func newSched(c *Core, fn func() config.Schedules) *Scheduler {
	return NewSchedulerLoc(c, fn, config.SchedLocation{Loc: time.Local}, config.NOAASun{})
}

func TestSchedulerCatchUpAppliesCurrentState(t *testing.T) {
	st := state.New()
	devs := mock.New()
	c := New(devs, st)
	go c.Run()
	defer c.Stop()

	sched := config.Schedule{Enabled: true, Entries: []config.ScheduleEntry{
		{At: "06:00", Action: "on", Brightness: 70},
		{At: "20:00", Action: "off"},
	}}
	s := newSched(c, func() config.Schedules { return config.Schedules{Light: sched} })
	// Noon local today => on@70.
	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.Local)
	s.CatchUpAtTime(now)

	for i := 0; i < 50; i++ {
		if st.Snapshot().Light.Brightness == 70 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Error("catch-up did not set brightness to 70")
}

func TestSchedulerSkipsDisabledChannel(t *testing.T) {
	st := state.New()
	c := New(mock.New(), st)
	go c.Run()
	defer c.Stop()
	sched := config.Schedule{Enabled: false, Entries: []config.ScheduleEntry{
		{At: "06:00", Action: "on", Brightness: 70},
	}}
	s := newSched(c, func() config.Schedules { return config.Schedules{Light: sched} })
	s.CatchUpAtTime(time.Date(2026, 6, 15, 12, 0, 0, 0, time.Local))

	time.Sleep(100 * time.Millisecond)
	if st.Snapshot().Light.Brightness != 0 {
		t.Error("disabled schedule should not drive the light")
	}
}

func TestSchedulerFiresDueBetweenInstants(t *testing.T) {
	st := state.New()
	c := New(mock.New(), st)
	go c.Run()
	defer c.Stop()
	sched := config.Schedule{Enabled: true, Entries: []config.ScheduleEntry{
		{At: "06:00", Action: "on", Brightness: 55},
	}}
	s := newSched(c, func() config.Schedules { return config.Schedules{Light: sched} })
	prev := time.Date(2026, 6, 15, 5, 59, 30, 0, time.Local)
	now := time.Date(2026, 6, 15, 6, 0, 30, 0, time.Local)
	s.fireDueBetween(prev, now)

	for i := 0; i < 50; i++ {
		if st.Snapshot().Light.Brightness == 55 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Error("due entry did not drive the light to 55")
}

func TestSchedulerTriggersFadeOnFadedOn(t *testing.T) {
	st := state.New()
	devs := mock.New()
	c := New(devs, st)
	go c.Run()
	defer c.Stop()
	sched := config.Schedule{Enabled: true, Entries: []config.ScheduleEntry{
		{At: "06:00", Action: "on", Brightness: 100, FadeMin: 5},
	}}
	s := newSched(c, func() config.Schedules { return config.Schedules{Light: sched} })
	prev := time.Date(2026, 6, 15, 5, 59, 30, 0, time.Local)
	now := time.Date(2026, 6, 15, 6, 0, 30, 0, time.Local)
	s.fireDueBetween(prev, now)

	// Fade should drive brightness up to 100 over its (test-scaled) steps.
	for i := 0; i < 200; i++ {
		if st.Snapshot().Light.Brightness == 100 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Errorf("faded on did not reach 100, got %d", st.Snapshot().Light.Brightness)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/core/ -run Scheduler -v`
Expected: build failure — `undefined: NewSchedulerLoc` / `CatchUpAtTime` / `fireDueBetween` (and `ApplyLightFade` once Step 3 lands; expected at this point: the `NewSchedulerLoc`/`CatchUpAtTime`/`fireDueBetween` undefineds).

- [ ] **Step 3: Rewrite the scheduler**

Replace `internal/core/scheduler.go`:
```go
package core

import (
	"sync"
	"time"

	"github.com/iot-root/garden-of-eden/internal/config"
)

type Scheduler struct {
	core     *Core
	schedFn  func() config.Schedules
	sl       config.SchedLocation
	sun      config.SunCalc
	done     chan struct{}
	stopOnce sync.Once
}

// NewScheduler keeps the Plan 3 signature, defaulting to local time and the NOAA
// calculator. Use NewSchedulerLoc to supply a location + SunCalc explicitly.
func NewScheduler(c *Core, schedFn func() config.Schedules) *Scheduler {
	return NewSchedulerLoc(c, schedFn, config.SchedLocation{Loc: time.Local}, config.NOAASun{})
}

func NewSchedulerLoc(c *Core, schedFn func() config.Schedules, sl config.SchedLocation, sun config.SunCalc) *Scheduler {
	if sl.Loc == nil {
		sl.Loc = time.Local
	}
	return &Scheduler{core: c, schedFn: schedFn, sl: sl, sun: sun, done: make(chan struct{})}
}

// CatchUpAt is retained for back-compat: it interprets nowMin as minutes-of-day
// on today's local date. Prefer CatchUpAtTime.
func (s *Scheduler) CatchUpAt(nowMin int) {
	now := localMidnightNow(s.sl.Loc).Add(time.Duration(nowMin) * time.Minute)
	s.CatchUpAtTime(now)
}

func localMidnightNow(loc *time.Location) time.Time {
	if loc == nil {
		loc = time.Local
	}
	y, m, d := time.Now().In(loc).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, loc)
}

func (s *Scheduler) CatchUpAtTime(now time.Time) {
	sc := s.schedFn()
	s.applyState(TargetLight, sc.Light, now)
	s.applyState(TargetPump, sc.Pump, now)
}

func (s *Scheduler) applyState(target Target, sch config.Schedule, now time.Time) {
	if !sch.Enabled {
		return
	}
	st, ok := sch.StateAtLoc(now, s.sl, s.sun)
	if !ok {
		return
	}
	if st.On {
		s.core.Submit(Command{Target: target, Action: ActionOn, Value: st.Brightness})
	} else {
		s.core.Submit(Command{Target: target, Action: ActionOff})
	}
}

// Run catches up, then every 15s fires entries due since the previous fire
// instant. Tracking the instant (not a minute) makes the loop DST-safe.
func (s *Scheduler) Run() {
	prev := time.Now()
	s.CatchUpAtTime(prev)
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-s.done:
			return
		case now := <-ticker.C:
			s.fireDueBetween(prev, now)
			prev = now
		}
	}
}

func (s *Scheduler) fireDueBetween(prev, now time.Time) {
	sc := s.schedFn()
	s.fireDue(TargetLight, sc.Light, prev, now)
	s.fireDue(TargetPump, sc.Pump, prev, now)
}

func (s *Scheduler) fireDue(target Target, sch config.Schedule, prev, now time.Time) {
	if !sch.Enabled {
		return
	}
	for _, e := range sch.DueBetweenLoc(prev, now, s.sl, s.sun) {
		if e.Action == "on" {
			if target == TargetLight && e.FadeMin > 0 {
				s.core.ApplyLightFade(e.Brightness, e.FadeMin)
				continue
			}
			s.core.Submit(Command{Target: target, Action: ActionOn, Value: e.Brightness})
		} else {
			s.core.Submit(Command{Target: target, Action: ActionOff})
		}
	}
}

func (s *Scheduler) Stop() { s.stopOnce.Do(func() { close(s.done) }) }
```

> `nowMinutes` (the old helper) is removed — no remaining caller. If any other file references it, this build will catch it; none does (only `scheduler.go` defined and used it).

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/core/ -run Scheduler -v`
Expected: PASS for all five scheduler tests (assumes Task 7’s `ApplyLightFade` is implemented; if running Steps 1–2 standalone before Task 7, temporarily expect a build failure on `ApplyLightFade` and complete Task 7 first).

---

### Task 6: Light fade — pure step math (`fadeSteps`)

**Depends on:** none (within this plan; new files only)

**Files:**
- Create: `internal/core/fade.go`
- Create: `internal/core/fade_test.go`

A pure function turns a fade request into a deterministic list of `(brightness, delay)` steps. Keeping it pure makes the ramp math unit-testable independent of timers and hardware. Design: one step per whole-percent change, evenly spaced across the fade duration, always ending exactly on the target.

- [ ] **Step 1: Write the failing tests**

`internal/core/fade_test.go`:
```go
package core

import (
	"testing"
	"time"
)

func TestFadeStepsRampUp(t *testing.T) {
	steps := fadeSteps(0, 100, 10) // 10 minutes
	if len(steps) == 0 {
		t.Fatal("no steps produced")
	}
	if steps[len(steps)-1].level != 100 {
		t.Errorf("final level = %d, want 100", steps[len(steps)-1].level)
	}
	// Monotonic non-decreasing levels.
	for i := 1; i < len(steps); i++ {
		if steps[i].level < steps[i-1].level {
			t.Errorf("non-monotonic at %d: %d < %d", i, steps[i].level, steps[i-1].level)
		}
	}
	// Total delay ≈ fade duration.
	var total time.Duration
	for _, s := range steps {
		total += s.delay
	}
	if total < 9*time.Minute || total > 11*time.Minute {
		t.Errorf("total fade duration = %s, want ~10m", total)
	}
}

func TestFadeStepsRampDown(t *testing.T) {
	steps := fadeSteps(80, 20, 6)
	if steps[len(steps)-1].level != 20 {
		t.Errorf("final = %d, want 20", steps[len(steps)-1].level)
	}
	for i := 1; i < len(steps); i++ {
		if steps[i].level > steps[i-1].level {
			t.Errorf("ramp-down not monotonic at %d", i)
		}
	}
}

func TestFadeStepsNoChange(t *testing.T) {
	// Same from/to: a single step at the target, no ramp.
	steps := fadeSteps(50, 50, 5)
	if len(steps) != 1 || steps[0].level != 50 {
		t.Errorf("steps = %+v, want one step @50", steps)
	}
}

func TestFadeStepsZeroDuration(t *testing.T) {
	// Zero/negative fade => single immediate step at the target.
	steps := fadeSteps(0, 70, 0)
	if len(steps) != 1 || steps[0].level != 70 || steps[0].delay != 0 {
		t.Errorf("zero-duration steps = %+v, want one immediate @70", steps)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/core/ -run Fade -v`
Expected: build failure — `undefined: fadeSteps`.

- [ ] **Step 3: Implement the pure step math**

`internal/core/fade.go`:
```go
package core

import "time"

// fadeStep is one brightness change applied after `delay` has elapsed since the
// previous step.
type fadeStep struct {
	level int
	delay time.Duration
}

// fadeSteps computes the brightness ramp from `from` to `to` over fadeMin
// minutes. It returns one step per whole-percent change, evenly spaced, ending
// exactly at `to`. A zero/negative duration or no-op delta yields a single
// immediate step at the target. Pure: no timers, no I/O.
func fadeSteps(from, to, fadeMin int) []fadeStep {
	if from == to || fadeMin <= 0 {
		return []fadeStep{{level: to, delay: 0}}
	}
	delta := to - from
	n := delta
	if n < 0 {
		n = -n
	}
	dir := 1
	if delta < 0 {
		dir = -1
	}
	total := time.Duration(fadeMin) * time.Minute
	per := total / time.Duration(n)
	steps := make([]fadeStep, 0, n)
	for i := 1; i <= n; i++ {
		steps = append(steps, fadeStep{level: from + dir*i, delay: per})
	}
	// Guarantee the final level is exactly `to` (it already is, but explicit).
	steps[len(steps)-1].level = to
	return steps
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/core/ -run Fade -v`
Expected:
```
--- PASS: TestFadeStepsRampUp (0.00s)
--- PASS: TestFadeStepsRampDown (0.00s)
--- PASS: TestFadeStepsNoChange (0.00s)
--- PASS: TestFadeStepsZeroDuration (0.00s)
PASS
```

---

### Task 7: Core fade execution + cancellation (-race)

**Depends on:** Task 6 (`fadeSteps`/`fadeStep`)

**Files:**
- Modify: `internal/core/core.go`
- Modify: `internal/core/core_test.go`

**Chosen design (justified):** A single fade goroutine driven by `fadeSteps`, submitting each `ActionSetLevel` step back through `Submit` so **all device mutation stays on the core goroutine** (the single-writer invariant from Plan 1). The fade goroutine only sleeps and submits; it never touches hardware directly. Cancellation uses a per-fade `chan struct{}` stored in `Core` under `cfgMu`: `ApplyLightFade` cancels any prior fade, then starts a new one; and **every** light/pump command cancels the active fade at the top of `applyLight`/`applyPump` (via `cancelFade`, which runs on the core goroutine). This guarantees a manual command instantly wins over an in-flight ramp, and `-race` stays clean because the cancel channel is the only cross-goroutine state and it is mutex-guarded.

Rejected alternative: `time.AfterFunc` per step. It scatters timers, complicates cancellation (must track N timers), and risks a late timer firing after cancel; one goroutine with a `select` on the cancel channel is simpler and race-free.

**Test-time scaling:** Fades use minutes in production. Tests must not wait minutes. We make the per-step sleep come from a `fadeTick` field defaulting to `per` from `fadeSteps`, but overridable. Simplest race-free approach: add a `Core.fadeSpeedup time.Duration`-free design — instead, expose a package var `fadeStepCap` that clamps each step delay (used only by tests). See implementation.

- [ ] **Step 1: Write the failing tests (append to `internal/core/core_test.go`)**

```go
func TestApplyLightFadeReachesTarget(t *testing.T) {
	// Cap each fade step to 1ms so the test is fast.
	old := fadeStepCap
	fadeStepCap = time.Millisecond
	defer func() { fadeStepCap = old }()

	st := state.New()
	devs := mock.New()
	c := New(devs, st)
	go c.Run()
	defer c.Stop()

	c.ApplyLightFade(100, 5) // ramp 0 -> 100 over (capped) steps

	reachedMid := false
	for i := 0; i < 500; i++ {
		b := st.Snapshot().Light.Brightness
		if b > 0 && b < 100 {
			reachedMid = true
		}
		if b == 100 {
			if !reachedMid {
				t.Error("fade jumped to target without intermediate steps")
			}
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Errorf("fade never reached 100, last=%d", st.Snapshot().Light.Brightness)
}

func TestNewCommandCancelsFade(t *testing.T) {
	old := fadeStepCap
	fadeStepCap = 5 * time.Millisecond
	defer func() { fadeStepCap = old }()

	st := state.New()
	devs := mock.New()
	c := New(devs, st)
	go c.Run()
	defer c.Stop()

	c.ApplyLightFade(100, 30) // long ramp
	time.Sleep(15 * time.Millisecond)
	// Interrupt with a manual off.
	c.Submit(Command{Target: TargetLight, Action: ActionOff})

	// Give the canceled fade a chance to (wrongly) keep going.
	time.Sleep(60 * time.Millisecond)
	if st.Snapshot().Light.On {
		t.Errorf("light On after off; fade was not cancelled (brightness=%d)",
			st.Snapshot().Light.Brightness)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/core/ -run 'ApplyLightFade|NewCommandCancelsFade' -v`
Expected: build failure — `undefined: fadeStepCap` / `c.ApplyLightFade undefined`.

- [ ] **Step 3: Implement fade execution + cancellation in `core.go`**

Add the package var and fields. Near the top of `core.go` (after imports):
```go
// fadeStepCap, when > 0, clamps each fade step's sleep. Production leaves it 0
// (use the natural per-step delay); tests set it small to run fast.
var fadeStepCap time.Duration
```

Add a field to `Core` (in the `cfgMu`-guarded group):
```go
	fadeCancel chan struct{} // guarded by cfgMu; closed to cancel an active fade
```

Add the fade entrypoint + helpers:
```go
// ApplyLightFade ramps the light to `target` over fadeMin minutes. It cancels
// any in-progress fade, then launches a goroutine that submits stepped
// ActionSetLevel commands through the core (preserving single-writer mutation).
// Any subsequent light/pump command cancels the fade via cancelFade.
func (c *Core) ApplyLightFade(target, fadeMin int) {
	from := 0
	if c.dev.Light != nil {
		from = c.dev.Light.Brightness()
	}
	steps := fadeSteps(from, target, fadeMin)

	cancel := make(chan struct{})
	c.cfgMu.Lock()
	if c.fadeCancel != nil {
		close(c.fadeCancel)
	}
	c.fadeCancel = cancel
	c.cfgMu.Unlock()

	go func() {
		for _, s := range steps {
			d := s.delay
			if fadeStepCap > 0 && (d > fadeStepCap || d == 0) {
				d = fadeStepCap
			}
			if d > 0 {
				select {
				case <-cancel:
					return
				case <-c.done:
					return
				case <-time.After(d):
				}
			}
			select {
			case <-cancel:
				return
			default:
			}
			c.Submit(Command{Target: TargetLight, Action: ActionSetLevel, Value: s.level})
		}
	}()
}

// cancelFade stops any active fade. Runs on the core goroutine (from applyLight/
// applyPump) so a manual command instantly supersedes a ramp.
func (c *Core) cancelFade() {
	c.cfgMu.Lock()
	if c.fadeCancel != nil {
		close(c.fadeCancel)
		c.fadeCancel = nil
	}
	c.cfgMu.Unlock()
}
```

Cancel the fade at the top of `applyLight` and `applyPump`. In `applyLight`, add as the first line of the method body (before the `switch`):
```go
func (c *Core) applyLight(cmd Command) {
	if cmd.Action != ActionSetLevel {
		// A fade drives the light via ActionSetLevel; only NON-fade commands
		// (manual on/off, or a new fade) cancel an in-progress ramp.
		c.cancelFade()
	}
	switch cmd.Action {
	// ... unchanged ...
	}
}
```

> Why exclude `ActionSetLevel` from cancelling: the fade itself submits `ActionSetLevel` steps; if those cancelled the fade, the first step would kill the ramp. Manual brightness sets also use `ActionSetLevel` — for those we accept that a manual mid-ramp `SetLevel` does not cancel (the ramp continues and overrides it). This is an intentional, documented tradeoff: manual on/off cleanly cancels; a manual exact-level set during a fade is a rare case and the next on/off resets it. `ApplyLightFade` already cancels the previous fade explicitly, so back-to-back fades are correct.

In `applyPump`, add as the first line of the method body:
```go
func (c *Core) applyPump(cmd Command) {
	c.cancelFade() // any pump action cancels a light fade per Decision 4
	switch cmd.Action {
	// ... unchanged ...
	}
}
```

- [ ] **Step 4: Run test to verify it passes (with race detector)**

Run: `go test ./internal/core/ -run 'ApplyLightFade|NewCommandCancelsFade' -race -v`
Expected:
```
--- PASS: TestApplyLightFadeReachesTarget (...)
--- PASS: TestNewCommandCancelsFade (...)
PASS
```

- [ ] **Step 5: Run full core suite with race detector**

Run: `go test ./internal/core/ -race -v`
Expected: all core tests (Plan 3 interlock/failsafe/over-temp, scheduler, fade) PASS, no races.

---

### Task 8: Wire location + SunCalc into the scheduler in `main.go`; full suite; single commit

**Depends on:** Task 3 (`Zone`/`LocationConfig`/`HasSolarEntry`), Task 5 (`NewSchedulerLoc`), Task 7 (`ApplyLightFade`)

**Files:**
- Modify: `cmd/gardynd/main.go`

- [ ] **Step 1: Build the scheduler with location + SunCalc**

In `cmd/gardynd/main.go`, replace the scheduler construction block:
```go
	sched := core.NewScheduler(c, getSchedules)
	go sched.Run()
	defer sched.Stop()
```
with:
```go
	if cfg.HasSolarEntry() && cfg.Location.Latitude == 0 && cfg.Location.Longitude == 0 {
		log.Printf("warning: solar schedule entries present but location lat/lon unset; "+
			"solar times will be computed at (0,0). Set LAT/LON or config location.")
	}
	sl := config.SchedLocation{
		Loc: cfg.Zone(),
		Lat: cfg.Location.Latitude,
		Lon: cfg.Location.Longitude,
	}
	sched := core.NewSchedulerLoc(c, getSchedules, sl, config.NOAASun{})
	go sched.Run()
	defer sched.Stop()
```

No other main.go changes are needed: `config` is already imported, `log` is already imported, and the fade trigger lives inside the scheduler.

- [ ] **Step 2: Build both targets**

Run: `make tidy && make build && make build-pi`
Expected: both host and Pi binaries build with no errors.

- [ ] **Step 3: Run the full test suite with the race detector**

Run: `go test ./... -race`
Expected: all packages PASS, no races. Specifically confirm `internal/config` (schedule, solar, time-eval, location, config) and `internal/core` (fade, scheduler, interlock) pass.

- [ ] **Step 4: Smoke-test the mock binary**

Seed `/tmp/g.yaml`:
```yaml
location:
  latitude: 40.7128
  longitude: -74.0060
  timezone: America/New_York
schedules:
  light:
    enabled: true
    entries:
      - {at: "00:01", action: "on", brightness: 60, fade_min: 1}
      - {solar: "sunset", solar_offset_min: -15, action: "off", days: ["mon","tue","wed","thu","fri"]}
```
Run `./bin/gardynd --hw=mock --config /tmp/g.yaml`. Confirm:
- `GET /state` shows the light on (catch-up from the past `00:01` on-entry, ramped by fade);
- `PUT /schedules/light` with a body containing `days`/`solar`/`fade_min` returns 200 and persists to `/tmp/g.yaml`;
- a `PUT` body with `solar: "noon"` returns `{"error": ...}` (validation rejects it);
- a `PUT` body with `days: ["funday"]` returns `{"error": ...}`.

- [ ] **Step 5: Commit (single commit covering the whole plan)**

```
git add internal/config/ internal/core/ cmd/gardynd/main.go
git commit -m "feat: richer schedules (weekday/solar/fade) + DST-correct time-aware scheduler"
```

---

## Self-Review

**1. Spec coverage**

| Requirement (Plan 09 scope) | Task |
|---|---|
| Item #11a per-weekday entries (`Days`, validate tokens, weekday filtering) | Task 1 (fields/validate), Task 4 (filtering) |
| Item #11b solar entries (`Solar`/`SolarOffsetMin`, NOAA calculator, `SunCalc`) | Task 1 (fields/validate), Task 2 (calculator), Task 4 (resolution) |
| Item #11b config `LocationConfig` (lat/lon/tz, LAT/LON/TZ env) + solar-requires-location | Task 3 |
| Item #11c light fade/ramp (`FadeMin`, pure step math, cancellable execution) | Task 6 (pure math), Task 7 (execution+cancel), Task 5 (trigger) |
| Item #12 DST-correct time-aware evaluation (`StateAtTime`/`DueBetweenTimes`, `SunCalc`) | Task 4 |
| Scheduler refactor to time-based API tracking previous *instant*; keep catch-up | Task 5 |
| Backward compatibility for `at:`-only entries | Task 1 (omitempty, validate unchanged for plain), Task 4 (`StateAtTime` back-compat test), Task 5 (kept `CatchUpAt`/old minute methods) |
| DST-boundary test (`America/New_York`) | Task 4 (`TestDueBetweenTimesDSTSpringForward`) |
| Decision 5: extend write/validation contract; HA mirror as follow-up | Task 1 (Validate), header cross-branch note (no `custom_components/` edits) |
| Full suite `go test ./... -race` + ONE commit | Task 8 |

No gaps. The minute-based `StateAt`/`DueBetween` (Plan 3) are deliberately retained and their tests left untouched — explicit per the "be explicit about existing tests" instruction. The scheduler tests in `scheduler_test.go` were migrated to the time-based API (documented in Task 5).

**2. Placeholder scan**

No "TBD"/"TODO"/"implement later". Every code step is complete, compilable Go. Every test step has an exact `go test` command and expected output. The only forward references are the deliberate, documented Task 5↔Task 7 ordering note (with a concrete workaround) and the cross-branch HA follow-up (explicitly out of scope).

**3. Type consistency**

- `ScheduleEntry{At, Action, Brightness, Days, Solar, SolarOffsetMin, FadeMin}` (Task 1) used identically in Tasks 4, 5, 8.
- `SunCalc` interface + `NOAASun` (Task 2) used in Tasks 4, 5, 8 and the config `Zone`/`HasSolarEntry` neighborhood.
- `SchedLocation{Loc, Lat, Lon}` (Task 4) used by `StateAtLoc`/`DueBetweenLoc` (Task 4), `NewSchedulerLoc` (Task 5), and main (Task 8).
- `Config.Zone() *time.Location`, `Config.HasSolarEntry()`, `LocationConfig{Latitude, Longitude, TimeZone}` (Task 3) used in main (Task 8). The field/method name collision (`Location`) is resolved by naming the resolver `Zone()` — flagged in Task 3 Step 2 with the exact test edit.
- `fadeStep{level, delay}` + `fadeSteps(from, to, fadeMin)` (Task 6) consumed by `ApplyLightFade` (Task 7).
- `Core.ApplyLightFade(target, fadeMin int)` (Task 7) called by `Scheduler.fireDue` (Task 5) and tests.
- `fadeStepCap` package var (Task 7) referenced only by core tests (Task 7).
- Scheduler API: `NewScheduler` (kept, Plan 3 signature), `NewSchedulerLoc`, `CatchUpAt` (kept), `CatchUpAtTime`, `fireDueBetween`, `Run`, `Stop` — consistent across `scheduler.go` and `scheduler_test.go`.

**4. Dependency audit**

- **Task 1** (`schedule.go` fields/validate): additive, no other task's interface — `none` correct. (Task 4 modifies the same file but depends on Task 1, not vice versa.)
- **Task 2** (`solar.go` new files): `none` correct.
- **Task 3** (`config.go`): touches only config; `none` correct. (Tests reference `NOAASun`? No — Task 3 tests only use `Zone`. Correct.)
- **Task 4** (`schedule.go` + `timeeval_test.go`): depends on Task 1 (fields/`parseDay`/`HasSolar`), Task 2 (`SunCalc`/`NOAASun`), Task 3 (`Zone` used in tests). Shares `schedule.go` with Task 1 → ordered after Task 1. Correct.
- **Task 5** (`scheduler.go`): depends on Task 4 (`StateAtLoc`/`DueBetweenLoc`/`SchedLocation`) and Task 7 (`ApplyLightFade`). The Task 5↔Task 7 ordering is called out with a split workaround. Correct.
- **Task 6** (`fade.go` new files): `none` correct.
- **Task 7** (`core.go`): depends on Task 6 (`fadeSteps`/`fadeStep`). Shares `core.go` with no other task in this plan. Correct.
- **Task 8** (`main.go`): depends on Tasks 3, 5, 7. Correct.

Parallelizable disjoint tasks: **Task 2** (`solar.go`), **Task 6** (`fade.go`), and **Task 3** (`config.go`) touch disjoint files and depend on nothing within the plan — they can run concurrently. Task 1 is independent of those three too (different concern in `schedule.go`), so Tasks 1/2/3/6 form the parallel front; Task 4 → Task 5 (+Task 7) → Task 8 serialize the rest.
