// Package config loads gardynd configuration from an optional YAML file,
// then applies environment-variable overrides for .env compatibility.
package config

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"

	"gopkg.in/yaml.v3"
)

type HTTPConfig struct {
	Port        int    `yaml:"port"`
	BindAddress string `yaml:"bind_address"`
	AuthToken   string `yaml:"auth_token"`
}

type DeviceConfig struct {
	Identifier string `yaml:"identifier"`
	Model      string `yaml:"model"`
	Version    string `yaml:"version"`
}

type CameraConfig struct {
	UpperDevice     string `yaml:"upper_device"`
	LowerDevice     string `yaml:"lower_device"`
	Resolution      string `yaml:"resolution"`
	IntervalSeconds int    `yaml:"interval_seconds"`
}

type WaterConfig struct {
	LowCM              float64 `yaml:"low_cm"` // 0 disables the interlock
	BlockOnSensorError bool    `yaml:"block_on_sensor_error"`
	// BlockOnSensorError, when true (default), refuses to start the pump if the
	// distance sensor errors while the interlock is enabled (fail-closed: a
	// dry-run guard that cannot read the level should not run the pump). Set
	// false to fail-open and pump anyway when the sensor is unreadable.
}

type PumpConfig struct {
	// MaxRuntimeSeconds bounds a single continuous pump run; 0 disables the
	// failsafe. Default 600 (10 minutes).
	MaxRuntimeSeconds int `yaml:"max_runtime_seconds"`
	// StateFile persists the pump-on start time so max-runtime can be enforced
	// across a crash/restart (the in-process timer dies with the process).
	StateFile string `yaml:"state_file"`
}

type OverTempConfig struct {
	CutLight bool `yaml:"cut_light"`
}

type Config struct {
	HTTP                     HTTPConfig     `yaml:"http"`
	Device                   DeviceConfig   `yaml:"device"`
	Camera                   CameraConfig   `yaml:"camera"`
	SensorType               string         `yaml:"sensor_type"`
	Schedules                Schedules      `yaml:"schedules"`
	Water                    WaterConfig    `yaml:"water"`
	Pump                     PumpConfig     `yaml:"pump"`
	OverTemp                 OverTempConfig `yaml:"overtemp"`
	TelemetryIntervalSeconds int            `yaml:"telemetry_interval_seconds"`
	LogLevel                 string         `yaml:"log_level"`
}

func defaults() Config {
	return Config{
		HTTP:   HTTPConfig{Port: 5000},
		Device: DeviceConfig{Identifier: "gardyn-xx", Model: "gardyn 3.0", Version: "1.0.0"},
		Camera: CameraConfig{
			UpperDevice:     "/dev/video0",
			LowerDevice:     "/dev/video2",
			Resolution:      "640x480",
			IntervalSeconds: 3600,
		},
		SensorType:               "AM2320",
		Water:                    WaterConfig{BlockOnSensorError: true},
		Pump:                     PumpConfig{MaxRuntimeSeconds: 600, StateFile: "/run/gardynd/pump.json"},
		TelemetryIntervalSeconds: 30,
		LogLevel:                 "info",
	}
}

// LoadFileOnly reads defaults overlaid with the YAML file at path (if any),
// WITHOUT applying environment-variable overrides. Use this as the baseline
// when saving, so runtime env/flag overrides are never written back to disk.
func LoadFileOnly(path string) (Config, error) {
	c := defaults()
	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return Config{}, fmt.Errorf("read config: %w", err)
		}
		if err := yaml.Unmarshal(data, &c); err != nil {
			return Config{}, fmt.Errorf("parse config: %w", err)
		}
	}
	if c.Camera.IntervalSeconds <= 0 {
		c.Camera.IntervalSeconds = 3600
	}
	return c, nil
}

// Load reads defaults, overlays the YAML file at path (if non-empty), then
// applies environment-variable overrides.
func Load(path string) (Config, error) {
	c, err := LoadFileOnly(path)
	if err != nil {
		return Config{}, err
	}
	applyEnv(&c)
	if c.Camera.IntervalSeconds <= 0 {
		c.Camera.IntervalSeconds = 3600
	}
	if c.TelemetryIntervalSeconds < 1 {
		c.TelemetryIntervalSeconds = 1
	}
	return c, nil
}

func applyEnv(c *Config) {
	envInt(&c.HTTP.Port, "HTTP_PORT")
	envStr(&c.HTTP.BindAddress, "HTTP_BIND_ADDRESS")
	envStr(&c.HTTP.AuthToken, "HTTP_AUTH_TOKEN")
	envStr(&c.Device.Identifier, "MQTT_IDENTIFIER") // legacy key name retained
	envStr(&c.Device.Model, "MQTT_DEVICE_MODEL")
	envStr(&c.Device.Version, "MQTT_VERSION")
	envStr(&c.Camera.UpperDevice, "UPPER_CAMERA_DEVICE")
	envStr(&c.Camera.LowerDevice, "LOWER_CAMERA_DEVICE")
	envStr(&c.Camera.Resolution, "CAMERA_RESOLUTION")
	envInt(&c.Camera.IntervalSeconds, "IMAGE_INTERVAL_SECONDS")
	envStr(&c.SensorType, "SENSOR_TYPE")
	envFloat(&c.Water.LowCM, "WATER_LOW_CM")
	envBool(&c.Water.BlockOnSensorError, "WATER_BLOCK_ON_SENSOR_ERROR")
	envInt(&c.Pump.MaxRuntimeSeconds, "PUMP_MAX_RUNTIME_SECONDS")
	envStr(&c.Pump.StateFile, "PUMP_STATE_FILE")
	envInt(&c.TelemetryIntervalSeconds, "TELEMETRY_INTERVAL_SECONDS")
	envStr(&c.LogLevel, "LOG_LEVEL")
}

func (c Config) Save(path string) error {
	data, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".config-*.yaml")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}

func envStr(dst *string, key string) {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		*dst = v
	}
}

func envInt(dst *int, key string) {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			*dst = n
		}
	}
}

func envFloat(dst *float64, key string) {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			*dst = f
		}
	}
}

func envBool(dst *bool, key string) {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			*dst = b
		}
	}
}

// ParseLogLevel converts a string level name ("debug", "info", "warn", "error",
// case-insensitive) to a slog.Level. Unknown or empty strings default to
// slog.LevelInfo.
func ParseLogLevel(s string) slog.Level {
	switch s {
	case "debug", "DEBUG":
		return slog.LevelDebug
	case "info", "INFO":
		return slog.LevelInfo
	case "warn", "WARN", "warning", "WARNING":
		return slog.LevelWarn
	case "error", "ERROR":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
