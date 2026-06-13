package core

import (
	"sync"
	"time"

	"github.com/iot-root/garden-of-eden/internal/config"
)

type Scheduler struct {
	core     *Core
	schedFn  func() config.Schedules
	done     chan struct{}
	stopOnce sync.Once
}

func NewScheduler(c *Core, schedFn func() config.Schedules) *Scheduler {
	return &Scheduler{core: c, schedFn: schedFn, done: make(chan struct{})}
}

// nowMinutes returns LOCAL wall-clock minutes since midnight. Because it uses local
// time, DST transitions can cause an entry near the transition to fire twice (fall-back)
// or be skipped (spring-forward).
func nowMinutes(t time.Time) int { return t.Hour()*60 + t.Minute() }

func (s *Scheduler) CatchUpAt(nowMin int) {
	sc := s.schedFn()
	s.applyState(TargetLight, sc.Light, nowMin)
	s.applyState(TargetPump, sc.Pump, nowMin)
}

func (s *Scheduler) applyState(target Target, sch config.Schedule, nowMin int) {
	if !sch.Enabled {
		return
	}
	st, ok := sch.StateAt(nowMin)
	if !ok {
		return
	}
	if st.On {
		s.core.Submit(Command{Target: target, Action: ActionOn, Value: st.Brightness})
	} else {
		s.core.Submit(Command{Target: target, Action: ActionOff})
	}
}

// Run does an initial catch-up then fires due entries each minute boundary.
func (s *Scheduler) Run() {
	now := time.Now()
	s.CatchUpAt(nowMinutes(now))
	prev := nowMinutes(now)
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-s.done:
			return
		case now := <-ticker.C:
			cur := nowMinutes(now)
			if cur == prev {
				continue
			}
			sc := s.schedFn()
			s.fireDue(TargetLight, sc.Light, prev, cur)
			s.fireDue(TargetPump, sc.Pump, prev, cur)
			prev = cur
		}
	}
}

func (s *Scheduler) fireDue(target Target, sch config.Schedule, prev, cur int) {
	if !sch.Enabled {
		return
	}
	for _, e := range sch.DueBetween(prev, cur) {
		if e.Action == "on" {
			s.core.Submit(Command{Target: target, Action: ActionOn, Value: e.Brightness})
		} else {
			s.core.Submit(Command{Target: target, Action: ActionOff})
		}
	}
}

func (s *Scheduler) Stop() { s.stopOnce.Do(func() { close(s.done) }) }
