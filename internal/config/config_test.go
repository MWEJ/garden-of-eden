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
