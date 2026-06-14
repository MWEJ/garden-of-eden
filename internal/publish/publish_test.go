package publish

import (
	"testing"

	"github.com/iot-root/garden-of-eden/internal/hw/mock"
	"github.com/iot-root/garden-of-eden/internal/state"
)

func TestPublishOnceUpdatesStore(t *testing.T) {
	devs := mock.New()
	st := state.New()
	frames := state.NewFrames()
	p := New(devs, st, frames, 0, nil)
	p.publishOnce()

	snap := st.Snapshot()
	if snap.Sensors.TemperatureC == nil || snap.Sensors.HumidityPct == nil {
		t.Error("temperature/humidity not set in snapshot")
	}
	if snap.Sensors.WaterLevelCM == nil {
		t.Error("water level not set")
	}
	if snap.Sensors.Pump == nil {
		t.Error("pump power not set")
	}
	if snap.Sensors.PCBTempC == nil {
		t.Error("PCB temp not set in snapshot")
	}
}

func TestCaptureUpdatesFrames(t *testing.T) {
	devs := mock.New()
	p := New(devs, state.New(), state.NewFrames(), 0, nil)
	p.captureOnce()
	if len(p.frames.Upper()) == 0 {
		t.Error("upper frame not captured")
	}
}

func TestPublishOnceReportsOverTemp(t *testing.T) {
	devs := mock.New()
	devs.PCBTemp.(*mock.PCBTemp).Over = true
	var got bool
	var called bool
	p := New(devs, state.New(), state.NewFrames(), 0, func(over bool) { called = true; got = over })
	p.publishOnce()
	if !called || !got {
		t.Errorf("over-temp callback: called=%v got=%v, want called&&true", called, got)
	}
}

func TestPublishOnceRecomputesWaterLowAboveThreshold(t *testing.T) {
	devs := mock.New()
	// Default mock Distance returns 7.5 cm when CM == 0.
	// Set it explicitly above threshold: distance > threshold => water is low.
	devs.Distance.(*mock.Distance).CM = 15.0

	st := state.New()
	// Pre-set threshold of 10 cm: any distance > 10 means the reservoir is low.
	st.SetWater(10.0, false) // threshold=10, low=false (stale)

	p := New(devs, st, state.NewFrames(), 0, nil)
	p.publishOnce()

	snap := st.Snapshot()
	if !snap.Water.Low {
		t.Errorf("water.low = false, want true (distance 15 > threshold 10)")
	}
	if snap.Water.LowThresholdCM != 10.0 {
		t.Errorf("LowThresholdCM = %v, want 10.0", snap.Water.LowThresholdCM)
	}
}

func TestPublishOnceRecomputesWaterLowBelowThreshold(t *testing.T) {
	devs := mock.New()
	// Distance below threshold: distance <= threshold => water is OK.
	devs.Distance.(*mock.Distance).CM = 5.0

	st := state.New()
	// Pre-set threshold and stale low=true.
	st.SetWater(10.0, true) // threshold=10, low=true (stale)

	p := New(devs, st, state.NewFrames(), 0, nil)
	p.publishOnce()

	snap := st.Snapshot()
	if snap.Water.Low {
		t.Errorf("water.low = true, want false (distance 5 <= threshold 10)")
	}
}

func TestPublishOnceSkipsWaterLowWhenThresholdZero(t *testing.T) {
	devs := mock.New()
	devs.Distance.(*mock.Distance).CM = 20.0 // very large distance

	st := state.New()
	// Threshold of 0 means disabled — do not touch water.low.
	st.SetWater(0, false)

	p := New(devs, st, state.NewFrames(), 0, nil)
	p.publishOnce()

	snap := st.Snapshot()
	if snap.Water.Low {
		t.Errorf("water.low = true, want false (threshold disabled)")
	}
}
