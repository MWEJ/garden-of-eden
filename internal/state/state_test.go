package state

import (
	"encoding/json"
	"testing"
)

func TestStoreSnapshotJSON(t *testing.T) {
	s := New()
	s.SetLight(true, 70)
	s.SetPump(false, 100)

	b, err := json.Marshal(s.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	light := got["light"].(map[string]any)
	if light["on"] != true || light["brightness"].(float64) != 70 {
		t.Errorf("light = %v", light)
	}
	if got["available"] != true {
		t.Errorf("available = %v", got["available"])
	}
}

func TestConcurrentWrites(t *testing.T) {
	s := New()
	done := make(chan struct{})
	go func() {
		for i := 0; i < 1000; i++ {
			s.SetLight(true, i%101)
		}
		close(done)
	}()
	for i := 0; i < 1000; i++ {
		_ = s.Snapshot()
	}
	<-done
}

func TestConcurrentScheduleWriteAndMarshal(t *testing.T) {
	s := New()
	done := make(chan struct{})
	go func() {
		for i := 0; i < 1000; i++ {
			s.SetScheduleEnabled("light", i%2 == 0)
		}
		close(done)
	}()
	for i := 0; i < 1000; i++ {
		if _, err := json.Marshal(s.Snapshot()); err != nil {
			t.Fatal(err)
		}
	}
	<-done
}

func TestSetDeviceInfo(t *testing.T) {
	s := New()
	s.SetDeviceInfo("gardyn-42", "gardyn 3.0", "2.1.0")
	snap := s.Snapshot()
	if snap.Identifier != "gardyn-42" {
		t.Errorf("Identifier = %q, want %q", snap.Identifier, "gardyn-42")
	}
	if snap.Model != "gardyn 3.0" {
		t.Errorf("Model = %q, want %q", snap.Model, "gardyn 3.0")
	}
	if snap.Version != "2.1.0" {
		t.Errorf("Version = %q, want %q", snap.Version, "2.1.0")
	}
}

func TestSetWaterSensorOK(t *testing.T) {
	s := New()
	s.SetWater(10.0, true) // threshold=10, low=true
	s.SetWaterSensorOK(false)
	snap := s.Snapshot()
	if snap.Water.SensorOK {
		t.Errorf("SensorOK = true, want false")
	}
	// SetWaterSensorOK must not clobber the threshold/low set by SetWater.
	if snap.Water.LowThresholdCM != 10.0 || !snap.Water.Low {
		t.Errorf("SetWaterSensorOK clobbered other fields: %+v", snap.Water)
	}
	s.SetWaterSensorOK(true)
	if !s.Snapshot().Water.SensorOK {
		t.Errorf("SensorOK = false after set true")
	}
}
