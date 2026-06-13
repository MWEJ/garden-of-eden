package core

import (
	"testing"
	"time"

	"github.com/iot-root/garden-of-eden/internal/config"
	"github.com/iot-root/garden-of-eden/internal/hw/mock"
	"github.com/iot-root/garden-of-eden/internal/state"
)

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
	s := NewScheduler(c, func() config.Schedules { return config.Schedules{Light: sched} })
	s.CatchUpAt(12 * 60) // noon => on@70

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
	s := NewScheduler(c, func() config.Schedules { return config.Schedules{Light: sched} })
	s.CatchUpAt(12 * 60)

	time.Sleep(100 * time.Millisecond)
	if st.Snapshot().Light.Brightness != 0 {
		t.Error("disabled schedule should not drive the light")
	}
}
