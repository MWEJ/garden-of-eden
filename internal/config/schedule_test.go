package config

import "testing"

func mins(h, m int) int { return h*60 + m }

func lightSched() Schedule {
	return Schedule{Enabled: true, Entries: []ScheduleEntry{
		{At: "06:00", Action: "on", Brightness: 70},
		{At: "09:00", Action: "off"},
		{At: "17:00", Action: "on", Brightness: 50},
		{At: "20:00", Action: "off"},
	}}
}

func TestStateAtMidTimeline(t *testing.T) {
	s := lightSched()
	st, ok := s.StateAt(mins(6, 5))
	if !ok || !st.On || st.Brightness != 70 {
		t.Errorf("06:05 => %+v ok=%v, want on@70", st, ok)
	}
	st, _ = s.StateAt(mins(12, 0))
	if st.On {
		t.Errorf("12:00 => %+v, want off", st)
	}
	st, _ = s.StateAt(mins(18, 0))
	if !st.On || st.Brightness != 50 {
		t.Errorf("18:00 => %+v, want on@50", st)
	}
}

func TestMidnightWrap(t *testing.T) {
	s := lightSched()
	st, _ := s.StateAt(mins(2, 0)) // before first entry => carried from 20:00 off
	if st.On {
		t.Errorf("02:00 => %+v, want off", st)
	}
}

func TestDueEntries(t *testing.T) {
	s := lightSched()
	due := s.DueBetween(mins(5, 59), mins(6, 0))
	if len(due) != 1 || due[0].Action != "on" {
		t.Errorf("due = %+v, want [on@06:00]", due)
	}
}

func TestValidate(t *testing.T) {
	good := Schedule{Entries: []ScheduleEntry{{At: "06:00", Action: "on", Brightness: 70}}}
	if err := good.Validate(); err != nil {
		t.Errorf("good schedule: unexpected error %v", err)
	}

	badTime := Schedule{Entries: []ScheduleEntry{{At: "25:99", Action: "on"}}}
	if err := badTime.Validate(); err == nil {
		t.Error("bad time \"25:99\": expected error, got nil")
	}

	badAction := Schedule{Entries: []ScheduleEntry{{At: "06:00", Action: "blink"}}}
	if err := badAction.Validate(); err == nil {
		t.Error("bad action \"blink\": expected error, got nil")
	}
}

func TestDueBetweenMidnightCrossing(t *testing.T) {
	s := Schedule{Enabled: true, Entries: []ScheduleEntry{
		{At: "00:00", Action: "off"},
		{At: "06:00", Action: "on", Brightness: 70},
		{At: "20:00", Action: "off"},
	}}

	// Crossing midnight: window is (23:59, 00:00] — should catch the 00:00 entry.
	due := s.DueBetween(mins(23, 59), mins(0, 0))
	if len(due) != 1 || due[0].Action != "off" || due[0].At != "00:00" {
		t.Errorf("midnight crossing: due = %+v, want [{00:00 off}]", due)
	}

	// Normal (non-crossing) window should still work: (05:59, 06:00].
	due2 := s.DueBetween(mins(5, 59), mins(6, 0))
	if len(due2) != 1 || due2[0].Action != "on" {
		t.Errorf("normal window: due = %+v, want [on@06:00]", due2)
	}

	// A normal window that does NOT contain 00:00 should not return it.
	due3 := s.DueBetween(mins(1, 0), mins(5, 0))
	if len(due3) != 0 {
		t.Errorf("window not containing 00:00: due = %+v, want []", due3)
	}
}

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
