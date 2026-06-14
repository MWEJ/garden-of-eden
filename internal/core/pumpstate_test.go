package core

import (
	"path/filepath"
	"testing"
	"time"
)

func TestPumpStateWriteReadClear(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "pump.json") // sub dir must be created
	started := time.Now().Truncate(time.Second)

	if err := writePumpState(path, started); err != nil {
		t.Fatalf("writePumpState: %v", err)
	}
	got, ok, err := readPumpState(path)
	if err != nil {
		t.Fatalf("readPumpState: %v", err)
	}
	if !ok {
		t.Fatal("readPumpState ok=false, want true after write")
	}
	if !got.Equal(started) {
		t.Errorf("startedAt = %v, want %v", got, started)
	}

	if err := clearPumpState(path); err != nil {
		t.Fatalf("clearPumpState: %v", err)
	}
	if _, ok, _ := readPumpState(path); ok {
		t.Error("readPumpState ok=true after clear, want false")
	}
}

func TestReadPumpStateMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent.json")
	_, ok, err := readPumpState(path)
	if err != nil {
		t.Fatalf("readPumpState missing: err = %v, want nil", err)
	}
	if ok {
		t.Error("ok = true for missing file, want false")
	}
}

func TestClearPumpStateMissingIsNoError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent.json")
	if err := clearPumpState(path); err != nil {
		t.Errorf("clearPumpState missing: err = %v, want nil", err)
	}
}

func TestShouldForceOff(t *testing.T) {
	start := time.Date(2026, 6, 13, 12, 0, 0, 0, time.UTC)
	max := 10 * time.Minute
	cases := []struct {
		name string
		now  time.Time
		max  time.Duration
		want bool
	}{
		{"elapsed below max", start.Add(5 * time.Minute), max, false},
		{"elapsed equals max", start.Add(10 * time.Minute), max, true},
		{"elapsed above max", start.Add(11 * time.Minute), max, true},
		{"max disabled (0)", start.Add(99 * time.Hour), 0, false},
		{"clock skew: now before start", start.Add(-time.Minute), max, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ShouldForceOff(start, tc.now, tc.max); got != tc.want {
				t.Errorf("ShouldForceOff(%v) = %v, want %v", tc.now, got, tc.want)
			}
		})
	}
}
