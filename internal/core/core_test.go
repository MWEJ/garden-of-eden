package core

import (
	"sync"
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

func TestPumpBlockedWhenWaterLow(t *testing.T) {
	st := state.New()
	devs := mock.New()
	devs.Distance.(*mock.Distance).CM = 12.0 // > threshold => too low
	c := New(devs, st)
	c.SetWaterLowCM(10.0)
	go c.Run()
	defer c.Stop()

	c.Submit(Command{Target: TargetPump, Action: ActionOn})

	// The interlock sets Water.Low=true before calling flashLights(), so it
	// becomes observable almost immediately while the pump is never driven.
	var lowSeen bool
	for i := 0; i < 50; i++ {
		if st.Snapshot().Water.Low {
			lowSeen = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !lowSeen {
		t.Fatal("water.low not set true")
	}
	if st.Snapshot().Pump.On {
		t.Error("pump turned on despite low water")
	}
	if devs.Pump.(*mock.Pump).Speed() != 0 {
		t.Error("pump hardware was driven")
	}
}

func TestPumpAllowedWhenWaterOK(t *testing.T) {
	st := state.New()
	devs := mock.New()
	devs.Distance.(*mock.Distance).CM = 5.0 // <= threshold => ok
	c := New(devs, st)
	c.SetWaterLowCM(10.0)
	go c.Run()
	defer c.Stop()

	c.Submit(Command{Target: TargetPump, Action: ActionOn})
	for i := 0; i < 50; i++ {
		if st.Snapshot().Pump.On {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Error("pump should have turned on")
}

func TestPumpFailsafeForcesOff(t *testing.T) {
	st := state.New()
	c := New(mock.New(), st)
	c.SetPumpMaxRuntime(50 * time.Millisecond)
	go c.Run()
	defer c.Stop()

	c.Submit(Command{Target: TargetPump, Action: ActionOn})
	for i := 0; i < 100; i++ {
		if !st.Snapshot().Pump.On {
			return // failsafe turned it off
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("failsafe did not turn pump off")
}

func TestSetWaterLowCMConcurrent(t *testing.T) {
	st := state.New()
	devs := mock.New()
	devs.Distance.(*mock.Distance).CM = 0.5 // below all nonzero thresholds => cm>threshold never true, no flashLights
	c := New(devs, st)
	c.SetPumpMaxRuntime(5 * time.Millisecond)
	go c.Run()
	defer c.Stop()

	var wg sync.WaitGroup
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				c.SetWaterLowCM(float64(g + i%5))
			}
		}(g)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			c.Submit(Command{Target: TargetPump, Action: ActionOn})
			c.Submit(Command{Target: TargetPump, Action: ActionOff})
		}
	}()
	wg.Wait()
}
