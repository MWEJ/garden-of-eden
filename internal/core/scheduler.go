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
