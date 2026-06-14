package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDefaults(t *testing.T) {
	c, err := Load("")
	if err != nil {
		t.Fatalf("Load(\"\"): %v", err)
	}
	if c.HTTP.Port != 5000 {
		t.Errorf("http port default = %d", c.HTTP.Port)
	}
	if c.Device.Identifier != "gardyn-xx" {
		t.Errorf("identifier default = %q", c.Device.Identifier)
	}
}

func TestFileThenEnvOverride(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	yaml := "http:\n  port: 5050\ndevice:\n  identifier: gardyn-01\n"
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MQTT_IDENTIFIER", "gardyn-env") // legacy env key still recognized

	c, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.HTTP.Port != 5050 { // file wins over default
		t.Errorf("port = %d, want 5050", c.HTTP.Port)
	}
	if c.Device.Identifier != "gardyn-env" { // env wins over file
		t.Errorf("identifier = %q, want gardyn-env", c.Device.Identifier)
	}
}

func TestCameraSensorDefaultsAndEnv(t *testing.T) {
	c, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if c.Camera.UpperDevice != "/dev/video0" || c.Camera.Resolution != "640x480" || c.Camera.IntervalSeconds != 3600 {
		t.Errorf("camera defaults: %+v", c.Camera)
	}
	t.Setenv("SENSOR_TYPE", "DHT20")
	t.Setenv("CAMERA_RESOLUTION", "1280x720")
	c, err = Load("")
	if err != nil {
		t.Fatal(err)
	}
	if c.SensorType != "DHT20" || c.Camera.Resolution != "1280x720" {
		t.Errorf("env override: SensorType=%q res=%q", c.SensorType, c.Camera.Resolution)
	}
}

func TestIntervalSecondsClampedPositive(t *testing.T) {
	t.Setenv("IMAGE_INTERVAL_SECONDS", "0")
	c, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if c.Camera.IntervalSeconds <= 0 {
		t.Errorf("IntervalSeconds = %d, want > 0 (clamped)", c.Camera.IntervalSeconds)
	}
}

func TestLoadFileOnlyIgnoresEnv(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("http:\n  port: 1234\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HTTP_PORT", "9999")

	fc, err := LoadFileOnly(path)
	if err != nil {
		t.Fatalf("LoadFileOnly: %v", err)
	}
	if fc.HTTP.Port != 1234 { // env ignored, file value preserved
		t.Errorf("LoadFileOnly port = %d, want 1234 (env must be ignored)", fc.HTTP.Port)
	}

	c, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.HTTP.Port != 9999 { // env applied at runtime
		t.Errorf("Load port = %d, want 9999 (env must be applied)", c.HTTP.Port)
	}
}

func TestAtomicSaveRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	c, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	c.Schedules.Light = Schedule{Enabled: true, Entries: []ScheduleEntry{{At: "06:00", Action: "on", Brightness: 70}}}
	c.Water.LowCM = 12.5
	c.OverTemp.CutLight = true
	if err := c.Save(path); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Schedules.Light.Enabled || len(got.Schedules.Light.Entries) != 1 {
		t.Errorf("round-trip lost schedule: %+v", got.Schedules.Light)
	}
	if got.Water.LowCM != 12.5 || !got.OverTemp.CutLight {
		t.Errorf("round-trip lost water/overtemp: water=%+v overtemp=%+v", got.Water, got.OverTemp)
	}
}

func TestTelemetryIntervalDefault(t *testing.T) {
	c, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if c.TelemetryIntervalSeconds != 30 {
		t.Errorf("TelemetryIntervalSeconds default = %d, want 30", c.TelemetryIntervalSeconds)
	}
}

func TestTelemetryIntervalEnvOverride(t *testing.T) {
	t.Setenv("TELEMETRY_INTERVAL_SECONDS", "120")
	c, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if c.TelemetryIntervalSeconds != 120 {
		t.Errorf("TelemetryIntervalSeconds = %d, want 120", c.TelemetryIntervalSeconds)
	}
}

func TestTelemetryIntervalClamped(t *testing.T) {
	t.Setenv("TELEMETRY_INTERVAL_SECONDS", "0")
	c, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if c.TelemetryIntervalSeconds < 1 {
		t.Errorf("TelemetryIntervalSeconds = %d after clamp, want >= 1", c.TelemetryIntervalSeconds)
	}
}

func TestPumpMaxRuntimeDefault(t *testing.T) {
	c, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if c.Pump.MaxRuntimeSeconds != 600 {
		t.Errorf("Pump.MaxRuntimeSeconds default = %d, want 600", c.Pump.MaxRuntimeSeconds)
	}
}

func TestPumpStateFileDefault(t *testing.T) {
	c, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if c.Pump.StateFile != "/run/gardynd/pump.json" {
		t.Errorf("Pump.StateFile default = %q, want /run/gardynd/pump.json", c.Pump.StateFile)
	}
}

func TestPumpMaxRuntimeEnvOverride(t *testing.T) {
	t.Setenv("PUMP_MAX_RUNTIME_SECONDS", "120")
	t.Setenv("PUMP_STATE_FILE", "/tmp/pump.json")
	c, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if c.Pump.MaxRuntimeSeconds != 120 {
		t.Errorf("Pump.MaxRuntimeSeconds = %d, want 120", c.Pump.MaxRuntimeSeconds)
	}
	if c.Pump.StateFile != "/tmp/pump.json" {
		t.Errorf("Pump.StateFile = %q, want /tmp/pump.json", c.Pump.StateFile)
	}
}

func TestBlockOnSensorErrorDefaultTrue(t *testing.T) {
	c, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if !c.Water.BlockOnSensorError {
		t.Errorf("Water.BlockOnSensorError default = false, want true (fail-closed)")
	}
}

func TestBlockOnSensorErrorEnvOverride(t *testing.T) {
	t.Setenv("WATER_BLOCK_ON_SENSOR_ERROR", "false")
	c, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if c.Water.BlockOnSensorError {
		t.Errorf("Water.BlockOnSensorError = true after env=false, want false")
	}
}
