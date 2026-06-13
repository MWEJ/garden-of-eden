// Package httpapi exposes the REST control + state surface, submitting commands
// to the core and serving the snapshot store.
package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/iot-root/garden-of-eden/internal/config"
	"github.com/iot-root/garden-of-eden/internal/core"
	"github.com/iot-root/garden-of-eden/internal/hw"
	"github.com/iot-root/garden-of-eden/internal/state"
)

// Handler builds the REST mux. Plan 3 extends it (schedules, sensors, cameras)
// via baseMux.
func Handler(c *core.Core, st *state.Store) http.Handler { return baseMux(c, st) }

func baseMux(c *core.Core, st *state.Store) *http.ServeMux {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /state", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, st.Snapshot())
	})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	mux.HandleFunc("POST /light/on", func(w http.ResponseWriter, _ *http.Request) {
		c.Submit(core.Command{Target: core.TargetLight, Action: core.ActionOn})
		writeJSON(w, http.StatusOK, map[string]string{"message": "Light turned on"})
	})
	mux.HandleFunc("POST /light/off", func(w http.ResponseWriter, _ *http.Request) {
		c.Submit(core.Command{Target: core.TargetLight, Action: core.ActionOff})
		writeJSON(w, http.StatusOK, map[string]string{"message": "Light turned off"})
	})
	mux.HandleFunc("POST /light/brightness", levelHandler(c, core.TargetLight))

	mux.HandleFunc("POST /pump/on", func(w http.ResponseWriter, _ *http.Request) {
		c.Submit(core.Command{Target: core.TargetPump, Action: core.ActionOn})
		writeJSON(w, http.StatusOK, map[string]string{"message": "Pump turned on!"})
	})
	mux.HandleFunc("POST /pump/off", func(w http.ResponseWriter, _ *http.Request) {
		c.Submit(core.Command{Target: core.TargetPump, Action: core.ActionOff})
		writeJSON(w, http.StatusOK, map[string]string{"message": "Pump turned off!"})
	})
	mux.HandleFunc("POST /pump/speed", levelHandler(c, core.TargetPump))

	return mux
}

func levelHandler(c *core.Core, target core.Target) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Value int `json:"value"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"message": "invalid JSON body"})
			return
		}
		if body.Value < 0 || body.Value > 100 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"message": "value must be 0..100"})
			return
		}
		c.Submit(core.Command{Target: target, Action: core.ActionSetLevel, Value: body.Value})
		writeJSON(w, http.StatusOK, map[string]int{"value": body.Value})
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// sensorMux returns the base control mux plus read-only on-demand sensor GET
// routes (parity with the old Flask API). Returns *http.ServeMux so later plans
// can extend it. Routes are only registered for sensors that are present.
func sensorMux(c *core.Core, st *state.Store, d hw.Devices) *http.ServeMux {
	mux := baseMux(c, st)

	if d.Distance != nil {
		mux.HandleFunc("GET /distance", func(w http.ResponseWriter, _ *http.Request) {
			v, err := d.Distance.MeasureCM()
			if err != nil {
				writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, map[string]float64{"distance": v})
		})
	}
	if d.Env != nil {
		mux.HandleFunc("GET /temperature", func(w http.ResponseWriter, _ *http.Request) {
			t, _, err := d.Env.Read()
			sensorFloat(w, "temperature", t, err)
		})
		mux.HandleFunc("GET /humidity", func(w http.ResponseWriter, _ *http.Request) {
			_, h, err := d.Env.Read()
			sensorFloat(w, "humidity", h, err)
		})
	}
	if d.PCBTemp != nil {
		mux.HandleFunc("GET /pcb-temp", func(w http.ResponseWriter, _ *http.Request) {
			t, err := d.PCBTemp.Temperature()
			sensorFloat(w, "pcb-temp", t, err)
		})
	}
	if d.Power != nil {
		mux.HandleFunc("GET /pump/stats", func(w http.ResponseWriter, _ *http.Request) {
			r, err := d.Power.Read()
			if err != nil {
				writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, http.StatusOK, r)
		})
	}
	return mux
}

func sensorFloat(w http.ResponseWriter, key string, v float64, err error) {
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{key: fmt.Sprintf("%.2f", v)})
}

// HandlerWithSensors is the control + sensor-read API (Plan 3 supersedes it
// with the full handler).
func HandlerWithSensors(c *core.Core, st *state.Store, d hw.Devices) http.Handler {
	return sensorMux(c, st, d)
}

// ControlDeps holds the injectable callbacks that connect the HTTP API to the
// config/persistence layer (schedule CRUD, water threshold).
type ControlDeps struct {
	GetSchedules       func() config.Schedules
	PutSchedule        func(channel string, s config.Schedule) error
	SetScheduleEnabled func(channel string, enabled bool) error
	SetWaterLowCM      func(cm float64) error
}

// HandlerFull is the complete API: base control + sensor reads + schedules +
// water threshold + cameras.
func HandlerFull(c *core.Core, st *state.Store, d hw.Devices, frames *state.Frames, deps ControlDeps) http.Handler {
	mux := sensorMux(c, st, d) // base + sensor GET routes (*http.ServeMux)

	mux.HandleFunc("GET /schedules", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, deps.GetSchedules())
	})
	mux.HandleFunc("PUT /schedules/{channel}", func(w http.ResponseWriter, r *http.Request) {
		ch := r.PathValue("channel")
		if ch != "light" && ch != "pump" {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown channel"})
			return
		}
		var s config.Schedule
		if err := json.NewDecoder(r.Body).Decode(&s); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
			return
		}
		if err := s.Validate(); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if err := deps.PutSchedule(ch, s); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "saved"})
	})
	mux.HandleFunc("POST /schedule/{channel}/enabled", func(w http.ResponseWriter, r *http.Request) {
		ch := r.PathValue("channel")
		if ch != "light" && ch != "pump" {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown channel"})
			return
		}
		var body struct {
			Enabled bool `json:"enabled"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
			return
		}
		if err := deps.SetScheduleEnabled(ch, body.Enabled); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		st.SetScheduleEnabled(ch, body.Enabled)
		writeJSON(w, http.StatusOK, map[string]bool{"enabled": body.Enabled})
	})
	mux.HandleFunc("POST /water/low-threshold", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			CM float64 `json:"cm"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.CM < 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "cm must be >= 0"})
			return
		}
		if err := deps.SetWaterLowCM(body.CM); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]float64{"cm": body.CM})
	})

	serveFrame := func(get func() []byte) http.HandlerFunc {
		return func(w http.ResponseWriter, _ *http.Request) {
			b := get()
			if len(b) == 0 {
				http.Error(w, "no frame yet", http.StatusServiceUnavailable)
				return
			}
			w.Header().Set("Content-Type", "image/jpeg")
			w.Header().Set("Cache-Control", "no-store")
			_, _ = w.Write(b)
		}
	}
	mux.HandleFunc("GET /camera/upper.jpg", serveFrame(frames.Upper))
	mux.HandleFunc("GET /camera/lower.jpg", serveFrame(frames.Lower))

	return mux
}
