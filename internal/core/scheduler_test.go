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
	old := fadeStepCap
	fadeStepCap = time.Millisecond
	defer func() { fadeStepCap = old }()

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
