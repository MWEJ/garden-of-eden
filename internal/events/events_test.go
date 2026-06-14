package events_test

import (
	"testing"
	"time"

	"github.com/iot-root/garden-of-eden/internal/events"
)

func TestRecordAndSnapshot(t *testing.T) {
	r := events.NewRecorder(10)
	r.Record("pump_on", "speed=80")
	r.Record("pump_off", "")

	snap := r.Snapshot()
	if len(snap) != 2 {
		t.Fatalf("len(snap) = %d, want 2", len(snap))
	}
	if snap[0].Kind != "pump_on" {
		t.Errorf("snap[0].Kind = %q, want %q", snap[0].Kind, "pump_on")
	}
	if snap[1].Kind != "pump_off" {
		t.Errorf("snap[1].Kind = %q, want %q", snap[1].Kind, "pump_off")
	}
	if snap[0].Time.IsZero() {
		t.Error("snap[0].Time is zero, want time.Now()-ish")
	}
}

func TestRingOverflow(t *testing.T) {
	r := events.NewRecorder(3)
	r.Record("a", "1")
	r.Record("b", "2")
	r.Record("c", "3")
	r.Record("d", "4") // overwrites "a"

	snap := r.Snapshot()
	if len(snap) != 3 {
		t.Fatalf("len(snap) = %d, want 3", len(snap))
	}
	// oldest-to-newest: b, c, d
	if snap[0].Kind != "b" || snap[1].Kind != "c" || snap[2].Kind != "d" {
		t.Errorf("snap kinds = [%q,%q,%q], want [b,c,d]", snap[0].Kind, snap[1].Kind, snap[2].Kind)
	}
}

func TestSnapshotOldestFirst(t *testing.T) {
	r := events.NewRecorder(5)
	for _, k := range []string{"x", "y", "z"} {
		r.Record(k, "")
		time.Sleep(time.Millisecond) // ensure ordering is observable via insertion order, not time
	}
	snap := r.Snapshot()
	if snap[0].Kind != "x" || snap[1].Kind != "y" || snap[2].Kind != "z" {
		t.Errorf("snap kinds = [%q,%q,%q], want [x,y,z]", snap[0].Kind, snap[1].Kind, snap[2].Kind)
	}
}

func TestSnapshotOnNilRecorder(t *testing.T) {
	// A nil *Recorder must not panic — core may call Record before SetEvents.
	var r *events.Recorder
	r.Record("anything", "") // must not panic
	snap := r.Snapshot()
	if snap != nil {
		t.Errorf("nil recorder Snapshot = %v, want nil", snap)
	}
}

func TestSnapshotIsACopy(t *testing.T) {
	r := events.NewRecorder(5)
	r.Record("first", "")
	snap := r.Snapshot()
	// Mutating the returned slice must not affect the ring.
	snap[0].Kind = "mutated"
	snap2 := r.Snapshot()
	if snap2[0].Kind != "first" {
		t.Errorf("ring was mutated by caller modifying snapshot: got %q", snap2[0].Kind)
	}
}
