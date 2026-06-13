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
