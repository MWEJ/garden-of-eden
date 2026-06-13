package config

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

type ScheduleEntry struct {
	At         string `yaml:"at" json:"at"`         // "HH:MM"
	Action     string `yaml:"action" json:"action"` // "on" | "off"
	Brightness int    `yaml:"brightness,omitempty" json:"brightness,omitempty"`
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

// Validate checks that every entry has a well-formed HH:MM time and a known
// action ("on" or "off").
func (s Schedule) Validate() error {
	for _, e := range s.Entries {
		if _, err := minutes(e.At); err != nil {
			return fmt.Errorf("entry at %q: %w", e.At, err)
		}
		if e.Action != "on" && e.Action != "off" {
			return fmt.Errorf("entry at %q: action must be on|off", e.At)
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
