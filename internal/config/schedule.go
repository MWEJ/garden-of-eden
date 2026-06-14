package config

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

type ScheduleEntry struct {
	At             string   `yaml:"at" json:"at"`                                     // "HH:MM" (ignored when Solar is set)
	Action         string   `yaml:"action" json:"action"`                             // "on" | "off"
	Brightness     int      `yaml:"brightness,omitempty" json:"brightness,omitempty"` // light only
	Days           []string `yaml:"days,omitempty" json:"days,omitempty"`             // "mon".."sun"; empty = every day
	Solar          string   `yaml:"solar,omitempty" json:"solar,omitempty"`           // "sunrise" | "sunset"; overrides At
	SolarOffsetMin int      `yaml:"solar_offset_min,omitempty" json:"solar_offset_min,omitempty"`
	FadeMin        int      `yaml:"fade_min,omitempty" json:"fade_min,omitempty"` // light "on" ramp duration
}

type Schedule struct {
	Enabled bool            `yaml:"enabled" json:"enabled"`
	Entries []ScheduleEntry `yaml:"entries" json:"entries"`
}

type Schedules struct {
	Light Schedule `yaml:"light" json:"light"`
	Pump  Schedule `yaml:"pump" json:"pump"`
}

type ChannelState struct {
	On         bool
	Brightness int
}

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

func minutes(at string) (int, error) {
	parts := strings.SplitN(at, ":", 2)
	if len(parts) != 2 {
		return 0, fmt.Errorf("bad time %q", at)
	}
	h, err1 := strconv.Atoi(parts[0])
	m, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil || h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, fmt.Errorf("bad time %q", at)
	}
	return h*60 + m, nil
}

// Validate checks that every entry is well-formed:
//   - solar entries: Solar must be "sunrise" or "sunset" (At is ignored);
//   - non-solar entries: At must parse as HH:MM;
//   - action must be "on" or "off";
//   - any Days tokens must be known 3-letter day names (case-insensitive);
//   - FadeMin must be >= 0 (SolarOffsetMin may be negative and is not bounded).
//
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

type parsedEntry struct {
	min   int
	entry ScheduleEntry
}

// sortedEntries intentionally re-parses on each call; cheap at tiny N and keeps Schedule stateless.
func (s Schedule) sortedEntries() []parsedEntry {
	var out []parsedEntry
	for _, e := range s.Entries {
		if m, err := minutes(e.At); err == nil {
			out = append(out, parsedEntry{m, e})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].min < out[j].min })
	return out
}

// StateAt returns the channel state implied at nowMin (minutes since midnight).
// When multiple entries share the same minute, the last one wins (highest index in sorted order).
func (s Schedule) StateAt(nowMin int) (ChannelState, bool) {
	es := s.sortedEntries()
	if len(es) == 0 {
		return ChannelState{}, false
	}
	idx := -1
	for i, e := range es {
		if e.min <= nowMin {
			idx = i
		}
	}
	if idx == -1 {
		idx = len(es) - 1 // carried from previous day
	}
	e := es[idx].entry
	return ChannelState{On: e.Action == "on", Brightness: e.Brightness}, true
}

// DueBetween returns entries with time in (prevMin, nowMin].
// When prevMin > nowMin the window crosses midnight (e.g. prevMin=1439, nowMin=0);
// in that case entries due are those with e.min > prevMin OR e.min <= nowMin.
func (s Schedule) DueBetween(prevMin, nowMin int) []ScheduleEntry {
	var out []ScheduleEntry
	for _, e := range s.sortedEntries() {
		var due bool
		if prevMin <= nowMin {
			// Normal window: no midnight crossing.
			due = e.min > prevMin && e.min <= nowMin
		} else {
			// Window crosses midnight.
			due = e.min > prevMin || e.min <= nowMin
		}
		if due {
			out = append(out, e.entry)
		}
	}
	return out
}

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

// effectiveEntries resolves all entries to their minute-of-day for the LOCAL
// date of `date`, filtering out entries whose Days set excludes that weekday.
// Plain entries resolve from At; solar entries from sun + SolarOffsetMin.
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

// SchedLocation bundles the location parameters time-aware evaluation needs.
// (Latitude/Longitude feed SunCalc; Loc handles wall-clock + DST.)
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
		return s.carryFromPriorDay(now, sl, sun, loc)
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
		if st, ok := s.carryFromPriorDay(now, sl, sun, loc); ok {
			return st, ok
		}
		idx = len(es) - 1
	}
	e := es[idx].entry
	return ChannelState{On: e.Action == "on", Brightness: e.Brightness}, true
}

// carryFromPriorDay returns the state implied by the last applicable entry on
// the most recent prior local day (up to 7 days back).
func (s Schedule) carryFromPriorDay(now time.Time, sl SchedLocation, sun SunCalc, loc *time.Location) (ChannelState, bool) {
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
		y, m, d := day.Date()
		for _, ee := range s.effectiveEntries(day, loc, sun, sl.Lat, sl.Lon) {
			// Build the entry's effective instant from its wall-clock time on this
			// local date. Using time.Date (not day.Add) means Go resolves the
			// wall-clock label through loc, so a DST spring-forward gap folds the
			// skipped hour to the adjacent valid instant and a fall-back duplicate
			// hour still maps to one instant — each entry fires exactly once.
			inst := time.Date(y, m, d, 0, ee.min, 0, 0, loc)
			if inst.After(prev) && !inst.After(now) {
				out = append(out, ee.entry)
			}
		}
		day = day.AddDate(0, 0, 1)
	}
	return out
}
