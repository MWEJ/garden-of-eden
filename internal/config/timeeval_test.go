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
