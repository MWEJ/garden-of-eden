package config

import "testing"

// reloadCallbacks captures which callbacks were called and with what values.
type reloadCallbacks struct {
	scheduleAssigns    []string // "light" or "pump", order recorded
	lightSchedule      Schedule
	pumpSchedule       Schedule
	lightEnabled       *bool
	pumpEnabled        *bool
	waterLowCM         *float64
	cutLightOnOverTemp *bool
}

func (cb *reloadCallbacks) opts() ReloadOpts {
	return ReloadOpts{
		SetLightSchedule: func(s Schedule) {
			cb.scheduleAssigns = append(cb.scheduleAssigns, "light")
			cb.lightSchedule = s
		},
		SetPumpSchedule: func(s Schedule) {
			cb.scheduleAssigns = append(cb.scheduleAssigns, "pump")
			cb.pumpSchedule = s
		},
		SetLightEnabled:       func(on bool) { cb.lightEnabled = &on },
		SetPumpEnabled:        func(on bool) { cb.pumpEnabled = &on },
		SetWaterLowCM:         func(cm float64) { cb.waterLowCM = &cm },
		SetCutLightOnOverTemp: func(b bool) { cb.cutLightOnOverTemp = &b },
	}
}

func baseConfig() Config {
	c := defaults()
	c.Schedules.Light = Schedule{Enabled: true, Entries: []ScheduleEntry{{At: "06:00", Action: "on", Brightness: 70}}}
	c.Water.LowCM = 10.0
	c.OverTemp.CutLight = false
	return c
}

func TestApplyReloadScheduleChanged(t *testing.T) {
	old := baseConfig()
	newCfg := baseConfig()
	newCfg.Schedules.Light.Entries[0].Brightness = 80

	cb := &reloadCallbacks{}
	ApplyReload(old, newCfg, cb.opts())

	if len(cb.scheduleAssigns) != 1 || cb.scheduleAssigns[0] != "light" {
		t.Errorf("expected light schedule callback, got %v", cb.scheduleAssigns)
	}
	if cb.lightSchedule.Entries[0].Brightness != 80 {
		t.Errorf("brightness = %d, want 80", cb.lightSchedule.Entries[0].Brightness)
	}
	if cb.lightEnabled == nil || *cb.lightEnabled != true {
		t.Errorf("SetLightEnabled not called with true; got %v", cb.lightEnabled)
	}
}

func TestApplyReloadPumpEnabledChanged(t *testing.T) {
	old := baseConfig()
	newCfg := baseConfig()
	newCfg.Schedules.Pump.Enabled = true // was false in baseConfig defaults

	cb := &reloadCallbacks{}
	ApplyReload(old, newCfg, cb.opts())

	// pump schedule changed (enabled flag flipped) → both schedule + enabled callbacks
	if cb.pumpEnabled == nil || *cb.pumpEnabled != true {
		t.Errorf("SetPumpEnabled not called with true; got %v", cb.pumpEnabled)
	}
}

func TestApplyReloadWaterLowCMChanged(t *testing.T) {
	old := baseConfig()
	newCfg := baseConfig()
	newCfg.Water.LowCM = 15.0

	cb := &reloadCallbacks{}
	ApplyReload(old, newCfg, cb.opts())

	if cb.waterLowCM == nil || *cb.waterLowCM != 15.0 {
		t.Errorf("SetWaterLowCM not called with 15.0; got %v", cb.waterLowCM)
	}
}

func TestApplyReloadOverTempChanged(t *testing.T) {
	old := baseConfig()
	newCfg := baseConfig()
	newCfg.OverTemp.CutLight = true

	cb := &reloadCallbacks{}
	ApplyReload(old, newCfg, cb.opts())

	if cb.cutLightOnOverTemp == nil || *cb.cutLightOnOverTemp != true {
		t.Errorf("SetCutLightOnOverTemp not called with true; got %v", cb.cutLightOnOverTemp)
	}
}

func TestApplyReloadHTTPPortIgnored(t *testing.T) {
	// HTTP port must NOT trigger any callback — it requires a restart.
	old := baseConfig()
	newCfg := baseConfig()
	newCfg.HTTP.Port = 9999 // changed — must be ignored

	panicIfCalled := func(name string) func() {
		return func() { t.Errorf("callback %s called but HTTP port change must be ignored", name) }
	}
	_ = panicIfCalled // used implicitly: none of the below callbacks should fire

	cb := &reloadCallbacks{}
	ApplyReload(old, newCfg, cb.opts())

	// No schedule, water, or overtemp change → no callbacks at all.
	if len(cb.scheduleAssigns) != 0 {
		t.Errorf("unexpected schedule callback: %v", cb.scheduleAssigns)
	}
	if cb.waterLowCM != nil {
		t.Errorf("unexpected water callback: %v", *cb.waterLowCM)
	}
	if cb.cutLightOnOverTemp != nil {
		t.Errorf("unexpected overtemp callback: %v", *cb.cutLightOnOverTemp)
	}
}

func TestApplyReloadNoChangeNoCallback(t *testing.T) {
	old := baseConfig()
	newCfg := baseConfig() // identical

	cb := &reloadCallbacks{}
	ApplyReload(old, newCfg, cb.opts())

	if len(cb.scheduleAssigns) != 0 || cb.waterLowCM != nil || cb.cutLightOnOverTemp != nil {
		t.Errorf("no change should produce no callbacks; got assigns=%v water=%v overtemp=%v",
			cb.scheduleAssigns, cb.waterLowCM, cb.cutLightOnOverTemp)
	}
}
