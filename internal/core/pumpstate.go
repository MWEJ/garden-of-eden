package core

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

// pumpRecord is the on-disk runtime state persisted while the pump is running.
// It lets a crashed/restarted process re-enforce the max-runtime failsafe (the
// in-process time.AfterFunc dies with the process).
type pumpRecord struct {
	StartedAt time.Time `json:"started_at"`
}

// writePumpState atomically records the pump-on start time at path (temp file in
// the same dir + rename, mirroring config.Save). It creates parent dirs as
// needed. Best-effort: callers log on error and never block pump operation.
func writePumpState(path string, startedAt time.Time) error {
	if path == "" {
		return nil
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(pumpRecord{StartedAt: startedAt})
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".pump-*.json")
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
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}

// readPumpState reads the persisted start time. ok=false (nil error) when the
// file does not exist. A malformed/unparseable file returns an error so the
// caller can log it and clear the file.
func readPumpState(path string) (startedAt time.Time, ok bool, err error) {
	if path == "" {
		return time.Time{}, false, nil
	}
	data, rerr := os.ReadFile(path)
	if rerr != nil {
		if errors.Is(rerr, os.ErrNotExist) {
			return time.Time{}, false, nil
		}
		return time.Time{}, false, rerr
	}
	var rec pumpRecord
	if jerr := json.Unmarshal(data, &rec); jerr != nil {
		return time.Time{}, false, jerr
	}
	if rec.StartedAt.IsZero() {
		return time.Time{}, false, nil
	}
	return rec.StartedAt, true, nil
}

// clearPumpState removes the runtime-state file. A missing file is not an error.
func clearPumpState(path string) error {
	if path == "" {
		return nil
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// ShouldForceOff reports whether a pump that started at startedAt has, by now,
// run at least maxRuntime and must be forced off. maxRuntime <= 0 disables the
// failsafe (returns false). Pure and unit-tested; safe against clock skew where
// now precedes startedAt (returns false).
func ShouldForceOff(startedAt, now time.Time, maxRuntime time.Duration) bool {
	if maxRuntime <= 0 {
		return false
	}
	elapsed := now.Sub(startedAt)
	if elapsed < 0 {
		return false
	}
	return elapsed >= maxRuntime
}
