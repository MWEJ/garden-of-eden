package config

import "testing"

func TestExampleConfigLoads(t *testing.T) {
	c, err := Load("../../config.example.yaml")
	if err != nil {
		t.Fatalf("example config failed to load: %v", err)
	}
	if !c.Schedules.Light.Enabled || len(c.Schedules.Pump.Entries) == 0 {
		t.Errorf("example schedules not parsed: %+v", c.Schedules)
	}
	if c.Camera.Resolution != "640x480" {
		t.Errorf("camera resolution = %q", c.Camera.Resolution)
	}
	if c.OverTemp.CutLight {
		t.Errorf("overtemp.cut_light = %v, want false", c.OverTemp.CutLight)
	}
	if c.Water.LowCM != 0 {
		t.Errorf("water.low_cm = %v, want 0", c.Water.LowCM)
	}
}
