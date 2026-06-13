// Package config loads gardynd configuration from an optional YAML file,
// then applies environment-variable overrides for .env compatibility.
package config

import (
	"fmt"
	"os"
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

type Config struct {
	HTTP       HTTPConfig   `yaml:"http"`
	Device     DeviceConfig `yaml:"device"`
	Camera     CameraConfig `yaml:"camera"`
	SensorType string       `yaml:"sensor_type"`
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

// Load reads defaults, overlays the YAML file at path (if non-empty), then
// applies environment-variable overrides.
func Load(path string) (Config, error) {
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
