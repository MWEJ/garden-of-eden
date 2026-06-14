package state

import (
	"encoding/json"
	"sync"
	"testing"
	"time"
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

func TestSubscribeReceivesNotifyOnSetLight(t *testing.T) {
	s := New()
	ch, cancel := s.Subscribe()
	defer cancel()

	s.SetLight(true, 80)

	select {
	case <-ch:
		// good — notification received
	case <-time.After(100 * time.Millisecond):
		t.Fatal("SetLight did not notify subscriber within 100ms")
	}
}

func TestSubscribeCancelUnregisters(t *testing.T) {
	s := New()
	ch, cancel := s.Subscribe()
	cancel()

	s.SetLight(true, 80)

	// After cancel the channel must not receive a new notification.
	select {
	case <-ch:
		t.Fatal("cancelled subscriber should not receive notifications")
	case <-time.After(50 * time.Millisecond):
		// good — silence after cancel
	}
}

func TestSubscribeFullChannelDoesNotBlockSetter(t *testing.T) {
	s := New()
	ch, cancel := s.Subscribe()
	defer cancel()

	// Fill the channel without draining it.
	s.SetLight(true, 50)  // first notify — fills the buffer
	s.SetPump(true, 100)  // second notify — channel full, must NOT block

	// The setter returned promptly (no deadlock). Drain once.
	select {
	case <-ch:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("channel should have had exactly one notification buffered")
	}
}

func TestMultipleSubscribers(t *testing.T) {
	s := New()
	ch1, cancel1 := s.Subscribe()
	ch2, cancel2 := s.Subscribe()
	defer cancel1()
	defer cancel2()

	s.SetPump(false, 0)

	var wg sync.WaitGroup
	wg.Add(2)
	recv := func(ch <-chan struct{}, name string) {
		defer wg.Done()
		select {
		case <-ch:
		case <-time.After(100 * time.Millisecond):
			t.Errorf("%s: did not receive notification", name)
		}
	}
	go recv(ch1, "sub1")
	go recv(ch2, "sub2")
	wg.Wait()
}
