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
