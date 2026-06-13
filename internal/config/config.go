// Package config loads gardynd configuration from an optional YAML file,
// then applies environment-variable overrides for .env compatibility.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"gopkg.in/yaml.v3"
)

type HTTPConfig struct {
	Port int `yaml:"port"`
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
	LowCM float64 `yaml:"low_cm"` // 0 disables the interlock
}

type OverTempConfig struct {
	CutLight bool `yaml:"cut_light"`
}

type Config struct {
	HTTP       HTTPConfig     `yaml:"http"`
	Device     DeviceConfig   `yaml:"device"`
	Camera     CameraConfig   `yaml:"camera"`
	SensorType string         `yaml:"sensor_type"`
	Schedules  Schedules      `yaml:"schedules"`
	Water      WaterConfig    `yaml:"water"`
	OverTemp   OverTempConfig `yaml:"overtemp"`
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
		SensorType: "AM2320",
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
	return c, nil
}

func applyEnv(c *Config) {
	envInt(&c.HTTP.Port, "HTTP_PORT")
	envStr(&c.Device.Identifier, "MQTT_IDENTIFIER") // legacy key name retained
	envStr(&c.Device.Model, "MQTT_DEVICE_MODEL")
	envStr(&c.Device.Version, "MQTT_VERSION")
	envStr(&c.Camera.UpperDevice, "UPPER_CAMERA_DEVICE")
	envStr(&c.Camera.LowerDevice, "LOWER_CAMERA_DEVICE")
	envStr(&c.Camera.Resolution, "CAMERA_RESOLUTION")
	envInt(&c.Camera.IntervalSeconds, "IMAGE_INTERVAL_SECONDS")
	envStr(&c.SensorType, "SENSOR_TYPE")
	envFloat(&c.Water.LowCM, "WATER_LOW_CM")
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
