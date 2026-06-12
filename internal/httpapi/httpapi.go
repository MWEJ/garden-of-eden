// Package httpapi exposes the REST control + state surface, submitting commands
// to the core and serving the snapshot store.
package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/iot-root/garden-of-eden/internal/core"
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
