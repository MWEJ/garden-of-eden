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
