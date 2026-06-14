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
