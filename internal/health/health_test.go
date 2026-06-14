package health_test

import (
	"testing"
	"time"

	"github.com/iot-root/garden-of-eden/internal/health"
)

func TestTouchAndAge(t *testing.T) {
	tr := health.NewTracker()
	before := time.Now()
	tr.Touch("env")
	after := time.Now()

	age, ok := tr.SensorAge("env", after)
	if !ok {
		t.Fatal("SensorAge ok = false after Touch")
	}
	if age < 0 || age > after.Sub(before).Seconds()+0.01 {
		t.Errorf("SensorAge = %.4f, expected ~0", age)
	}
}

func TestNeverTouched(t *testing.T) {
	tr := health.NewTracker()
	_, ok := tr.SensorAge("pcb_temp", time.Now())
	if ok {
		t.Error("SensorAge ok = true for sensor that was never touched")
	}
}

func TestSnapshotContainsAllTouched(t *testing.T) {
	tr := health.NewTracker()
	tr.Touch("env")
	tr.Touch("distance")

	snap := tr.Snapshot(time.Now())
	if _, ok := snap["env"]; !ok {
		t.Error("snapshot missing 'env'")
	}
	if _, ok := snap["distance"]; !ok {
		t.Error("snapshot missing 'distance'")
	}
	// A sensor we never touched must not appear.
	if _, ok := snap["pump_power"]; ok {
		t.Error("snapshot has 'pump_power' which was never touched")
	}
}

func TestSnapshotOKWhenFresh(t *testing.T) {
	tr := health.NewTracker()
	tr.Touch("env")
	snap := tr.Snapshot(time.Now())
	if !snap["env"].OK {
		t.Error("env should be OK (just touched)")
	}
}

func TestSnapshotNotOKWhenStale(t *testing.T) {
	tr := health.NewTracker()
	// Fake a read that happened 200 seconds ago.
	past := time.Now().Add(-200 * time.Second)
	tr.TouchAt("env", past)

	snap := tr.Snapshot(time.Now())
	if snap["env"].OK {
		t.Errorf("env should be stale (age=200s > staleness window 120s), got OK=true")
	}
}

func TestConcurrentTouch(t *testing.T) {
	tr := health.NewTracker()
	done := make(chan struct{})
	for i := 0; i < 8; i++ {
		go func() {
			for j := 0; j < 200; j++ {
				tr.Touch("env")
				tr.Snapshot(time.Now())
			}
			done <- struct{}{}
		}()
	}
	for i := 0; i < 8; i++ {
		<-done
	}
}
