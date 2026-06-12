package core

import (
	"testing"
	"time"

	"github.com/iot-root/garden-of-eden/internal/hw/mock"
	"github.com/iot-root/garden-of-eden/internal/state"
)

func waitLight(st *state.Store, want int) bool {
	for i := 0; i < 50; i++ {
		if st.Snapshot().Light.Brightness == want {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

func TestLightOnUpdatesSnapshot(t *testing.T) {
	st := state.New()
	c := New(mock.New(), st)
	go c.Run()
	defer c.Stop()

	c.Submit(Command{Target: TargetLight, Action: ActionOn, Value: 70})

	if !waitLight(st, 70) {
		t.Errorf("light brightness not 70; snapshot=%+v", st.Snapshot().Light)
	}
	if !st.Snapshot().Light.On {
		t.Error("light.on not true")
	}
}

func TestPumpOffUpdatesSnapshot(t *testing.T) {
	st := state.New()
	c := New(mock.New(), st)
	go c.Run()
	defer c.Stop()

	c.Submit(Command{Target: TargetPump, Action: ActionOff})
	for i := 0; i < 50; i++ {
		if !st.Snapshot().Pump.On {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Error("pump.on stayed true")
}

func TestStopIdempotent(t *testing.T) {
	c := New(mock.New(), state.New())
	go c.Run()
	c.Stop()
	c.Stop() // must not panic on double Stop
}

func TestSubmitAfterStopDoesNotBlock(t *testing.T) {
	c := New(mock.New(), state.New())
	go c.Run()
	c.Stop()
	time.Sleep(20 * time.Millisecond) // let Run observe done and exit

	completed := make(chan struct{})
	go func() {
		for i := 0; i < 100; i++ {
			c.Submit(Command{Target: TargetLight, Action: ActionOn})
		}
		close(completed)
	}()
	select {
	case <-completed:
	case <-time.After(2 * time.Second):
		t.Fatal("Submit blocked after Stop")
	}
}
