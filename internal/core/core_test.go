package core

import (
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/iot-root/garden-of-eden/internal/events"
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

func TestPumpBlockedOnSensorErrorFailClosed(t *testing.T) {
	st := state.New()
	devs := mock.New()
	devs.Distance.(*mock.Distance).Err = errExampleSensor
	c := New(devs, st)
	c.SetWaterLowCM(10.0)
	c.SetBlockOnSensorError(true) // fail-closed
	go c.Run()
	defer c.Stop()

	c.Submit(Command{Target: TargetPump, Action: ActionOn})
	time.Sleep(2 * time.Second) // flashLights blocks ~1.8s

	snap := st.Snapshot()
	if snap.Pump.On {
		t.Error("pump turned on despite sensor error (fail-closed)")
	}
	if snap.Water.SensorOK {
		t.Error("water.sensor_ok should be false after a read error")
	}
	if devs.Pump.(*mock.Pump).Speed() != 0 {
		t.Error("pump hardware was driven despite sensor error")
	}
}

func TestPumpAllowedOnSensorErrorFailOpen(t *testing.T) {
	st := state.New()
	devs := mock.New()
	devs.Distance.(*mock.Distance).Err = errExampleSensor
	c := New(devs, st)
	c.SetWaterLowCM(10.0)
	c.SetBlockOnSensorError(false) // fail-open: pump anyway
	go c.Run()
	defer c.Stop()

	c.Submit(Command{Target: TargetPump, Action: ActionOn})
	for i := 0; i < 50; i++ {
		if st.Snapshot().Pump.On {
			if st.Snapshot().Water.SensorOK {
				t.Error("water.sensor_ok should be false even when failing open")
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Error("pump should have turned on (fail-open)")
}

func TestPumpSensorOKTrueOnGoodRead(t *testing.T) {
	st := state.New()
	devs := mock.New()
	devs.Distance.(*mock.Distance).CM = 5.0 // <= threshold => ok, no error
	c := New(devs, st)
	c.SetWaterLowCM(10.0)
	go c.Run()
	defer c.Stop()

	c.Submit(Command{Target: TargetPump, Action: ActionOn})
	for i := 0; i < 50; i++ {
		snap := st.Snapshot()
		if snap.Pump.On {
			if !snap.Water.SensorOK {
				t.Error("water.sensor_ok should be true after a good read")
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Error("pump should have turned on")
}

var errExampleSensor = errSensorTest{}

type errSensorTest struct{}

func (errSensorTest) Error() string { return "mock distance sensor error" }

func TestPumpOnPersistsStateOffClearsIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pump.json")
	st := state.New()
	c := New(mock.New(), st)
	c.SetPumpStateFile(path)
	c.SetPumpMaxRuntime(time.Hour) // long, so the failsafe does not fire mid-test
	go c.Run()
	defer c.Stop()

	c.Submit(Command{Target: TargetPump, Action: ActionOn})
	for i := 0; i < 50; i++ {
		if _, ok, _ := readPumpState(path); ok {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, ok, _ := readPumpState(path); !ok {
		t.Fatal("pump-on did not persist state file")
	}

	c.Submit(Command{Target: TargetPump, Action: ActionOff})
	for i := 0; i < 50; i++ {
		if _, ok, _ := readPumpState(path); !ok {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("pump-off did not clear state file")
}

func TestEnforcePumpRuntimeExpiredForcesOffAndClears(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pump.json")
	// Simulate a crash 20 minutes ago with a 10-minute max.
	if err := writePumpState(path, time.Now().Add(-20*time.Minute)); err != nil {
		t.Fatal(err)
	}
	st := state.New()
	devs := mock.New()
	_ = devs.Pump.(*mock.Pump).SetSpeed(100) // pretend hardware is still running
	c := New(devs, st)

	remaining := c.EnforcePumpRuntime(path, 10*time.Minute, time.Now())

	if remaining != 0 {
		t.Errorf("remaining = %v, want 0 (expired)", remaining)
	}
	if devs.Pump.(*mock.Pump).Speed() != 0 {
		t.Error("expired pump was not driven off")
	}
	if _, ok, _ := readPumpState(path); ok {
		t.Error("state file not cleared after expiry")
	}
}

func TestEnforcePumpRuntimeRemainingReturnsRemainder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pump.json")
	// Started 3 minutes ago with a 10-minute max => ~7 min remaining.
	if err := writePumpState(path, time.Now().Add(-3*time.Minute)); err != nil {
		t.Fatal(err)
	}
	c := New(mock.New(), state.New())
	remaining := c.EnforcePumpRuntime(path, 10*time.Minute, time.Now())
	if remaining <= 6*time.Minute || remaining > 7*time.Minute {
		t.Errorf("remaining = %v, want ~7m", remaining)
	}
	// File must remain so a later disarm/clear path can remove it.
	if _, ok, _ := readPumpState(path); !ok {
		t.Error("state file cleared while still within max runtime")
	}
}

func TestEnforcePumpRuntimeNoFileReturnsZero(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent.json")
	c := New(mock.New(), state.New())
	if got := c.EnforcePumpRuntime(path, 10*time.Minute, time.Now()); got != 0 {
		t.Errorf("remaining = %v, want 0 when no file", got)
	}
}

func TestInterlockBlockRecordsEvent(t *testing.T) {
	st := state.New()
	devs := mock.New()
	devs.Distance.(*mock.Distance).CM = 12.0 // > threshold => water low => interlock fires
	c := New(devs, st)
	c.SetWaterLowCM(10.0)

	rec := events.NewRecorder(20)
	c.SetEvents(rec)

	go c.Run()
	defer c.Stop()

	c.Submit(Command{Target: TargetPump, Action: ActionOn})

	// Wait for water.low to be set (interlock has fired).
	for i := 0; i < 50; i++ {
		if st.Snapshot().Water.Low {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !st.Snapshot().Water.Low {
		t.Fatal("interlock did not fire (water.low not set)")
	}

	snap := rec.Snapshot()
	found := false
	for _, ev := range snap {
		if ev.Kind == "interlock_block" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("interlock_block event not recorded; events = %+v", snap)
	}
}

func TestApplyLightFadeReachesTarget(t *testing.T) {
	// Cap each fade step to 1ms so the test is fast.
	old := fadeStepCap
	fadeStepCap = time.Millisecond
	defer func() { fadeStepCap = old }()

	st := state.New()
	devs := mock.New()
	c := New(devs, st)
	go c.Run()
	defer c.Stop()

	c.ApplyLightFade(100, 5) // ramp 0 -> 100 over (capped) steps

	reachedMid := false
	for i := 0; i < 500; i++ {
		b := st.Snapshot().Light.Brightness
		if b > 0 && b < 100 {
			reachedMid = true
		}
		if b == 100 {
			if !reachedMid {
				t.Error("fade jumped to target without intermediate steps")
			}
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Errorf("fade never reached 100, last=%d", st.Snapshot().Light.Brightness)
}

func TestNewCommandCancelsFade(t *testing.T) {
	old := fadeStepCap
	fadeStepCap = 5 * time.Millisecond
	defer func() { fadeStepCap = old }()

	st := state.New()
	devs := mock.New()
	c := New(devs, st)
	go c.Run()
	defer c.Stop()

	c.ApplyLightFade(100, 30) // long ramp
	time.Sleep(15 * time.Millisecond)
	// Interrupt with a manual off.
	c.Submit(Command{Target: TargetLight, Action: ActionOff})

	// Give the canceled fade a chance to (wrongly) keep going.
	time.Sleep(60 * time.Millisecond)
	if st.Snapshot().Light.On {
		t.Errorf("light On after off; fade was not cancelled (brightness=%d)",
			st.Snapshot().Light.Brightness)
	}
}

func TestPumpOnOffRecordsEvents(t *testing.T) {
	st := state.New()
	c := New(mock.New(), st)
	rec := events.NewRecorder(20)
	c.SetEvents(rec)
	go c.Run()
	defer c.Stop()

	c.Submit(Command{Target: TargetPump, Action: ActionOn})
	for i := 0; i < 50; i++ {
		if st.Snapshot().Pump.On {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	c.Submit(Command{Target: TargetPump, Action: ActionOff})
	for i := 0; i < 50; i++ {
		if !st.Snapshot().Pump.On {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	snap := rec.Snapshot()
	kinds := make(map[string]bool)
	for _, ev := range snap {
		kinds[ev.Kind] = true
	}
	if !kinds["pump_on"] {
		t.Errorf("pump_on event not recorded; events = %+v", snap)
	}
	if !kinds["pump_off"] {
		t.Errorf("pump_off event not recorded; events = %+v", snap)
	}
}
