// Package config — reload.go: pure helper for config hot-reload.
//
// configReloadInterval is the mtime-poll cadence used by the reloader goroutine
// in main. It is a constant rather than a config field because the reload interval
// cannot itself be hot-reloaded — changing it would require a restart — and
// hard-coding 5 s is fast enough for DX while cheap on a Pi Zero.
package config

import (
	"reflect"
	"time"
)

// ConfigReloadInterval is the mtime-poll cadence for the hot-reload goroutine.
// Exposed as a package-level constant so main can use it without duplication.
const ConfigReloadInterval = 5 * time.Second

// ReloadOpts holds injectable setter callbacks for every live-mutable field.
// Fields that require a restart (HTTP.Port, HTTP.Bind, all *IntervalSeconds)
// have no slot here — they are intentionally absent.
type ReloadOpts struct {
	// Schedule callbacks: called when the corresponding schedule differs.
	// The full new Schedule value is delivered; callers must also assign it
	// into their schedMu-guarded copy and call SetScheduleEnabled on the store.
	SetLightSchedule func(s Schedule)
	SetPumpSchedule  func(s Schedule)
	SetLightEnabled  func(on bool)
	SetPumpEnabled   func(on bool)
	// Water interlock threshold.
	SetWaterLowCM func(cm float64)
	// Over-temperature: whether to cut the light on alert.
	SetCutLightOnOverTemp func(b bool)
}

// ApplyReload compares oldCfg and newCfg (both from LoadFileOnly, never from
// Load, so env/flag overrides are excluded) and calls the ReloadOpts callbacks
// for every live-mutable field that changed. It is pure (no I/O, no goroutines)
// and is the correct unit to test.
//
// NOT hot-reloaded (require a restart): HTTP.Port, HTTP.BindAddress,
// Camera.*IntervalSeconds, TelemetryIntervalSeconds. Document this to operators
// in the config YAML.
func ApplyReload(oldCfg, newCfg Config, opts ReloadOpts) {
	// Schedules: compare with reflect.DeepEqual to catch entry-level changes.
	if !reflect.DeepEqual(oldCfg.Schedules.Light, newCfg.Schedules.Light) {
		if opts.SetLightSchedule != nil {
			opts.SetLightSchedule(newCfg.Schedules.Light)
		}
		if opts.SetLightEnabled != nil {
			opts.SetLightEnabled(newCfg.Schedules.Light.Enabled)
		}
	}
	if !reflect.DeepEqual(oldCfg.Schedules.Pump, newCfg.Schedules.Pump) {
		if opts.SetPumpSchedule != nil {
			opts.SetPumpSchedule(newCfg.Schedules.Pump)
		}
		if opts.SetPumpEnabled != nil {
			opts.SetPumpEnabled(newCfg.Schedules.Pump.Enabled)
		}
	}
	// Water interlock threshold.
	if oldCfg.Water.LowCM != newCfg.Water.LowCM {
		if opts.SetWaterLowCM != nil {
			opts.SetWaterLowCM(newCfg.Water.LowCM)
		}
	}
	// Over-temp cut-light.
	if oldCfg.OverTemp.CutLight != newCfg.OverTemp.CutLight {
		if opts.SetCutLightOnOverTemp != nil {
			opts.SetCutLightOnOverTemp(newCfg.OverTemp.CutLight)
		}
	}
}
