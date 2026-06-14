// Package state holds the thread-safe device snapshot served by GET /state.
package state

import (
	"sync"
	"time"
)

type LightState struct {
	On         bool `json:"on"`
	Brightness int  `json:"brightness"`
}

type PumpState struct {
	On    bool `json:"on"`
	Speed int  `json:"speed"`
}

type PumpPower struct {
	BusVoltage float64 `json:"bus_voltage"`
	Current    float64 `json:"current"`
	Power      float64 `json:"power"`
}

// Sensors uses pointers so an absent/failed sensor serializes as null.
type Sensors struct {
	TemperatureC *float64   `json:"temperature_c"`
	HumidityPct  *float64   `json:"humidity_pct"`
	PCBTempC     *float64   `json:"pcb_temp_c"`
	WaterLevelCM *float64   `json:"water_level_cm"`
	Pump         *PumpPower `json:"pump"`
}

type WaterState struct {
	LowThresholdCM float64 `json:"low_threshold_cm"`
	Low            bool    `json:"low"`
	SensorOK       bool    `json:"sensor_ok"`
}

type Snapshot struct {
	Available  bool                 `json:"available"`
	UptimeS    int64                `json:"uptime_s"`
	Identifier string               `json:"identifier"`
	Model      string               `json:"model"`
	Version    string               `json:"version"`
	Light      LightState           `json:"light"`
	Pump       PumpState            `json:"pump"`
	Sensors    Sensors              `json:"sensors"`
	Water      WaterState           `json:"water"`
	OverTemp   bool                 `json:"overtemp"`
	Schedules  map[string]SchedFlag `json:"schedules"`
}

type SchedFlag struct {
	Enabled bool `json:"enabled"`
}

type Store struct {
	mu    sync.RWMutex
	start time.Time
	snap  Snapshot
}

func New() *Store {
	return &Store{
		start: time.Now(),
		snap: Snapshot{
			Available: true,
			Schedules: map[string]SchedFlag{"light": {}, "pump": {}},
		},
	}
}

// Snapshot returns a copy with a freshly-computed uptime. The Schedules map is
// deep-copied so callers can marshal the result without holding the store lock.
func (s *Store) Snapshot() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	snap := s.snap
	snap.UptimeS = int64(time.Since(s.start).Seconds())
	if s.snap.Schedules != nil {
		snap.Schedules = make(map[string]SchedFlag, len(s.snap.Schedules))
		for k, v := range s.snap.Schedules {
			snap.Schedules[k] = v
		}
	}
	return snap
}

func (s *Store) SetLight(on bool, brightness int) {
	s.mu.Lock()
	s.snap.Light = LightState{On: on, Brightness: brightness}
	s.mu.Unlock()
}

func (s *Store) SetPump(on bool, speed int) {
	s.mu.Lock()
	s.snap.Pump = PumpState{On: on, Speed: speed}
	s.mu.Unlock()
}

func (s *Store) SetWater(thresholdCM float64, low bool) {
	s.mu.Lock()
	s.snap.Water = WaterState{LowThresholdCM: thresholdCM, Low: low}
	s.mu.Unlock()
}

// SetWaterSensorOK records whether the last distance-sensor read succeeded,
// without disturbing the threshold/low fields set by SetWater.
func (s *Store) SetWaterSensorOK(ok bool) {
	s.mu.Lock()
	s.snap.Water.SensorOK = ok
	s.mu.Unlock()
}

func (s *Store) SetOverTemp(v bool) { s.mu.Lock(); s.snap.OverTemp = v; s.mu.Unlock() }

// SetDeviceInfo records the device's unique identifier, model string, and
// firmware version in the snapshot. Call once at startup from main.
func (s *Store) SetDeviceInfo(identifier, model, version string) {
	s.mu.Lock()
	s.snap.Identifier = identifier
	s.snap.Model = model
	s.snap.Version = version
	s.mu.Unlock()
}

func (s *Store) SetScheduleEnabled(channel string, enabled bool) {
	s.mu.Lock()
	s.snap.Schedules[channel] = SchedFlag{Enabled: enabled}
	s.mu.Unlock()
}

// Sensor setters (used by Plan 3 publishers).
func (s *Store) SetTemperature(c float64) {
	s.mu.Lock()
	s.snap.Sensors.TemperatureC = &c
	s.mu.Unlock()
}
func (s *Store) SetHumidity(p float64) { s.mu.Lock(); s.snap.Sensors.HumidityPct = &p; s.mu.Unlock() }
func (s *Store) SetPCBTemp(c float64)  { s.mu.Lock(); s.snap.Sensors.PCBTempC = &c; s.mu.Unlock() }
func (s *Store) SetWaterLevel(cm float64) {
	s.mu.Lock()
	s.snap.Sensors.WaterLevelCM = &cm
	s.mu.Unlock()
}
func (s *Store) SetPumpPower(p PumpPower) { s.mu.Lock(); s.snap.Sensors.Pump = &p; s.mu.Unlock() }
