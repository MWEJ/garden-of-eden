// Package httpapi exposes the REST control + state surface, submitting commands
// to the core and serving the snapshot store.
package httpapi

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/iot-root/garden-of-eden/internal/config"
	"github.com/iot-root/garden-of-eden/internal/core"
	"github.com/iot-root/garden-of-eden/internal/events"
	"github.com/iot-root/garden-of-eden/internal/health"
	"github.com/iot-root/garden-of-eden/internal/hw"
	"github.com/iot-root/garden-of-eden/internal/state"
)

// Handler builds the REST mux. Plan 3 extends it (schedules, sensors, cameras)
// via baseMux.
func Handler(c *core.Core, st *state.Store) http.Handler {
	mux := baseMux(c, st)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	return mux
}

func baseMux(c *core.Core, st *state.Store) *http.ServeMux {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /state", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, st.Snapshot())
	})
	// Note: GET /healthz is NOT registered here. It is added by Handler (simple)
	// or HandlerFull (rich), to avoid duplicate registration panics.

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
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
			return
		}
		if body.Value < 0 || body.Value > 100 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "value must be 0..100"})
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
// water threshold + cameras + events + metrics + rich healthz.
func HandlerFull(c *core.Core, st *state.Store, d hw.Devices, frames *state.Frames, deps ControlDeps, rec *events.Recorder, tr *health.Tracker) http.Handler {
	mux := sensorMux(c, st, d) // base + sensor GET routes (*http.ServeMux)

	// GET /healthz — richer health check with per-sensor status.
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		snap := st.Snapshot()
		var sensors map[string]health.SensorStatus
		if tr != nil {
			sensors = tr.Snapshot(time.Now())
		} else {
			sensors = map[string]health.SensorStatus{}
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"status":   "ok",
			"uptime_s": snap.UptimeS,
			"sensors":  sensors,
		})
	})

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

	// GET /events — event ring buffer snapshot
	mux.HandleFunc("GET /events", func(w http.ResponseWriter, _ *http.Request) {
		var snap []events.Event
		if rec != nil {
			snap = rec.Snapshot()
		}
		if snap == nil {
			snap = []events.Event{} // encode as [] not null
		}
		writeJSON(w, http.StatusOK, snap)
	})

	// GET /metrics — hand-rolled Prometheus text exposition
	mux.HandleFunc("GET /metrics", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		w.WriteHeader(http.StatusOK)
		writeMetrics(w, st, rec)
	})

	return mux
}

// WithAuth wraps h with bearer-token authentication. When token is empty,
// auth is disabled and h is returned unchanged (backward-compatible).
// /healthz is always exempt so health checks work without credentials.
// Compare with crypto/subtle to avoid timing attacks.
//
// Future note: when /metrics is added (a later plan), add it to the exempt
// list here alongside /healthz.
func WithAuth(h http.Handler, token string) http.Handler {
	if token == "" {
		return h // auth disabled — pass-through
	}
	want := []byte("Bearer " + token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// /healthz is unconditionally exempt.
		if r.URL.Path == "/healthz" {
			h.ServeHTTP(w, r)
			return
		}
		got := []byte(strings.TrimSpace(r.Header.Get("Authorization")))
		if subtle.ConstantTimeCompare(got, want) != 1 {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		h.ServeHTTP(w, r)
	})
}

// writeMetrics writes the Prometheus text exposition format to w.
// Nil sensor pointer fields in the snapshot are silently skipped.
func writeMetrics(w http.ResponseWriter, st *state.Store, rec *events.Recorder) {
	snap := st.Snapshot()
	b := &strings.Builder{}

	gauge := func(name, help string, val float64) {
		fmt.Fprintf(b, "# HELP %s %s\n# TYPE %s gauge\n%s %.6g\n", name, help, name, name, val)
	}
	boolGauge := func(name, help string, v bool) {
		f := 0.0
		if v {
			f = 1.0
		}
		gauge(name, help, f)
	}
	counter := func(name, help string, val int64) {
		fmt.Fprintf(b, "# HELP %s %s\n# TYPE %s counter\n%s_total %d\n", name, help, name, name, val)
	}

	// Gauges from state snapshot.
	if snap.Sensors.TemperatureC != nil {
		gauge("gardynd_temperature_c", "Ambient temperature in Celsius.", *snap.Sensors.TemperatureC)
	}
	if snap.Sensors.HumidityPct != nil {
		gauge("gardynd_humidity_pct", "Ambient relative humidity percent.", *snap.Sensors.HumidityPct)
	}
	if snap.Sensors.PCBTempC != nil {
		gauge("gardynd_pcb_temp_c", "PCB temperature in Celsius.", *snap.Sensors.PCBTempC)
	}
	if snap.Sensors.WaterLevelCM != nil {
		gauge("gardynd_water_level_cm", "Distance sensor reading in cm (higher = lower water).", *snap.Sensors.WaterLevelCM)
	}
	if snap.Sensors.Pump != nil {
		gauge("gardynd_pump_bus_voltage", "Pump INA219 bus voltage in volts.", snap.Sensors.Pump.BusVoltage)
		gauge("gardynd_pump_current", "Pump INA219 current in amps.", snap.Sensors.Pump.Current)
		gauge("gardynd_pump_power", "Pump INA219 power in watts.", snap.Sensors.Pump.Power)
	}
	gauge("gardynd_uptime_seconds", "Seconds since gardynd started.", float64(snap.UptimeS))
	boolGauge("gardynd_pump_on", "1 if the pump is currently on.", snap.Pump.On)
	boolGauge("gardynd_light_on", "1 if the light is currently on.", snap.Light.On)
	boolGauge("gardynd_water_low", "1 if the water level is below the low threshold.", snap.Water.Low)
	boolGauge("gardynd_overtemp", "1 if an over-temperature condition is active.", snap.OverTemp)

	// Counters derived from the event ring buffer.
	var pumpRuns, interlockBlocks, failsafes, overtempEvents int64
	if rec != nil {
		for _, ev := range rec.Snapshot() {
			switch ev.Kind {
			case "pump_on":
				pumpRuns++
			case "interlock_block":
				interlockBlocks++
			case "pump_failsafe":
				failsafes++
			case "overtemp":
				overtempEvents++
			}
		}
	}
	counter("gardynd_pump_runs", "Total number of pump-on events since start (approximate: counts ring buffer window).", pumpRuns)
	counter("gardynd_interlock_blocks", "Total pump-on attempts blocked by the water-low interlock.", interlockBlocks)
	counter("gardynd_failsafe", "Total pump failsafe activations.", failsafes)
	counter("gardynd_overtemp_events", "Total over-temperature events recorded.", overtempEvents)

	_, _ = fmt.Fprint(w, b.String())
}
