package httpapi

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/iot-root/garden-of-eden/internal/config"
	"github.com/iot-root/garden-of-eden/internal/core"
	"github.com/iot-root/garden-of-eden/internal/events"
	"github.com/iot-root/garden-of-eden/internal/health"
	"github.com/iot-root/garden-of-eden/internal/hw"
	"github.com/iot-root/garden-of-eden/internal/hw/mock"
	"github.com/iot-root/garden-of-eden/internal/state"
)

func newH(t *testing.T) (http.Handler, *state.Store, func()) {
	st := state.New()
	c := core.New(mock.New(), st)
	go c.Run()
	return Handler(c, st), st, c.Stop
}

func TestLightOnThenState(t *testing.T) {
	h, st, stop := newH(t)
	defer stop()

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/light/brightness", strings.NewReader(`{"value":42}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("brightness status = %d", rec.Code)
	}

	// Poll /state until the command is applied.
	var snap state.Snapshot
	for i := 0; i < 50; i++ {
		rec = httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/state", nil))
		_ = json.Unmarshal(rec.Body.Bytes(), &snap)
		if snap.Light.Brightness == 42 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if snap.Light.Brightness != 42 {
		t.Errorf("state light brightness = %d, want 42", snap.Light.Brightness)
	}
	_ = st
}

func TestHealthz(t *testing.T) {
	h, _, stop := newH(t)
	defer stop()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("healthz status = %d", rec.Code)
	}
}

func TestBadBrightnessRejected(t *testing.T) {
	h, _, stop := newH(t)
	defer stop()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/light/brightness", strings.NewReader(`{"value":150}`)))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestDistanceRoute(t *testing.T) {
	st := state.New()
	devs := mock.New()
	c := core.New(devs, st)
	go c.Run()
	defer c.Stop()
	h := sensorMux(c, st, devs)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/distance", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "distance") {
		t.Errorf("distance route: %d %s", rec.Code, rec.Body.String())
	}
}

func TestSensorRouteAbsentWhenNil(t *testing.T) {
	st := state.New()
	devs := hw.Devices{Light: &mock.Light{}, Pump: &mock.Pump{}} // sensors deliberately nil
	c := core.New(devs, st)
	go c.Run()
	defer c.Stop()
	h := sensorMux(c, st, devs)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/distance", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("nil distance sensor: route should be absent (404), got %d", rec.Code)
	}
}

func TestSchedulePutGetAndEnable(t *testing.T) {
	st := state.New()
	c := core.New(mock.New(), st)
	go c.Run()
	defer c.Stop()

	store := config.Schedules{}
	deps := ControlDeps{
		GetSchedules: func() config.Schedules { return store },
		PutSchedule: func(ch string, s config.Schedule) error {
			if ch == "light" {
				store.Light = s
			} else {
				store.Pump = s
			}
			return nil
		},
		SetScheduleEnabled: func(ch string, on bool) error {
			if ch == "light" {
				store.Light.Enabled = on
			}
			return nil
		},
		SetWaterLowCM: func(cm float64) error { return nil },
	}
	h := HandlerFull(c, st, mock.New(), state.NewFrames(), deps, nil, nil)

	body := `{"enabled":true,"entries":[{"at":"06:00","action":"on","brightness":70}]}`
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/schedules/light", strings.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT status = %d body=%s", rec.Code, rec.Body.String())
	}
	if !store.Light.Enabled || len(store.Light.Entries) != 1 {
		t.Errorf("schedule not stored: %+v", store.Light)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/schedules", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "06:00") {
		t.Errorf("GET schedules = %d %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/schedule/light/enabled", strings.NewReader(`{"enabled":false}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST enabled: %d %s", rec.Code, rec.Body.String())
	}
	if store.Light.Enabled {
		t.Error("expected light schedule disabled after POST enabled=false")
	}
}

func TestScheduleUnknownChannel404(t *testing.T) {
	st := state.New()
	c := core.New(mock.New(), st)
	go c.Run()
	defer c.Stop()
	deps := ControlDeps{
		GetSchedules:       func() config.Schedules { return config.Schedules{} },
		PutSchedule:        func(string, config.Schedule) error { return nil },
		SetScheduleEnabled: func(string, bool) error { return nil },
		SetWaterLowCM:      func(float64) error { return nil },
	}
	h := HandlerFull(c, st, mock.New(), state.NewFrames(), deps, nil, nil)
	body := `{"enabled":true,"entries":[{"at":"06:00","action":"on"}]}`
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/schedules/bogus", strings.NewReader(body)))
	if rec.Code != http.StatusNotFound {
		t.Errorf("unknown channel: code=%d, want 404", rec.Code)
	}
}

func TestScheduleInvalidAction400(t *testing.T) {
	st := state.New()
	c := core.New(mock.New(), st)
	go c.Run()
	defer c.Stop()
	deps := ControlDeps{
		GetSchedules:       func() config.Schedules { return config.Schedules{} },
		PutSchedule:        func(string, config.Schedule) error { return nil },
		SetScheduleEnabled: func(string, bool) error { return nil },
		SetWaterLowCM:      func(float64) error { return nil },
	}
	h := HandlerFull(c, st, mock.New(), state.NewFrames(), deps, nil, nil)
	body := `{"enabled":true,"entries":[{"at":"06:00","action":"blink"}]}`
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/schedules/light", strings.NewReader(body)))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("invalid action: code=%d, want 400", rec.Code)
	}
}

func TestWaterThresholdEndpoint(t *testing.T) {
	st := state.New()
	c := core.New(mock.New(), st)
	go c.Run()
	defer c.Stop()
	var gotCM float64
	deps := ControlDeps{
		GetSchedules:       func() config.Schedules { return config.Schedules{} },
		PutSchedule:        func(string, config.Schedule) error { return nil },
		SetScheduleEnabled: func(string, bool) error { return nil },
		SetWaterLowCM:      func(cm float64) error { gotCM = cm; return nil },
	}
	h := HandlerFull(c, st, mock.New(), state.NewFrames(), deps, nil, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/water/low-threshold", strings.NewReader(`{"cm":8.5}`)))
	if rec.Code != http.StatusOK || gotCM != 8.5 {
		t.Errorf("threshold endpoint: code=%d gotCM=%v", rec.Code, gotCM)
	}
}

func TestCameraEndpoint(t *testing.T) {
	st := state.New()
	c := core.New(mock.New(), st)
	go c.Run()
	defer c.Stop()
	frames := state.NewFrames()
	frames.SetUpper([]byte{0xFF, 0xD8, 0xFF, 0xD9})
	deps := ControlDeps{
		GetSchedules:       func() config.Schedules { return config.Schedules{} },
		PutSchedule:        func(string, config.Schedule) error { return nil },
		SetScheduleEnabled: func(string, bool) error { return nil },
		SetWaterLowCM:      func(float64) error { return nil },
	}
	h := HandlerFull(c, st, mock.New(), frames, deps, nil, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/camera/upper.jpg", nil))
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "image/jpeg" {
		t.Errorf("camera endpoint: code=%d ct=%s", rec.Code, rec.Header().Get("Content-Type"))
	}
}

func TestBadBrightnessBodyHasErrorKey(t *testing.T) {
	h, _, stop := newH(t)
	defer stop()

	// Bad value (out of range): previously returned {"message": "value must be 0..100"}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/light/brightness", strings.NewReader(`{"value":999}`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if _, ok := body["error"]; !ok {
		t.Errorf("error body = %v, want key \"error\"", body)
	}
	if _, ok := body["message"]; ok {
		t.Errorf("error body still has legacy key \"message\": %v", body)
	}
}

func TestBadBrightnessInvalidJSONHasErrorKey(t *testing.T) {
	h, _, stop := newH(t)
	defer stop()

	// Malformed JSON: previously returned {"message": "invalid JSON body"}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/pump/speed", strings.NewReader(`not-json`)))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if _, ok := body["error"]; !ok {
		t.Errorf("error body = %v, want key \"error\"", body)
	}
}

func TestLightOnSuccessBodyUnchanged(t *testing.T) {
	h, _, stop := newH(t)
	defer stop()

	// Success response must still use "message", not "error"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/light/on", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if msg, ok := body["message"]; !ok || msg != "Light turned on" {
		t.Errorf("success body = %v, want {\"message\":\"Light turned on\"}", body)
	}
}

func newFullH(t *testing.T) (http.Handler, *events.Recorder, *health.Tracker, func()) {
	t.Helper()
	st := state.New()
	// Pre-populate sensor readings so /metrics can emit gardynd_temperature_c etc.
	temp := 22.5
	st.SetTemperature(temp)
	c := core.New(mock.New(), st)
	go c.Run()
	rec := events.NewRecorder(100)
	tr := health.NewTracker()
	deps := ControlDeps{
		GetSchedules:       func() config.Schedules { return config.Schedules{} },
		PutSchedule:        func(string, config.Schedule) error { return nil },
		SetScheduleEnabled: func(string, bool) error { return nil },
		SetWaterLowCM:      func(float64) error { return nil },
	}
	h := HandlerFull(c, st, mock.New(), state.NewFrames(), deps, rec, tr)
	return h, rec, tr, c.Stop
}

func TestGetEvents(t *testing.T) {
	h, rec, _, stop := newFullH(t)
	defer stop()

	rec.Record("pump_on", "speed=100")
	rec.Record("pump_off", "")

	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, httptest.NewRequest(http.MethodGet, "/events", nil))
	if rec2.Code != http.StatusOK {
		t.Fatalf("/events status = %d, want 200", rec2.Code)
	}
	ct := rec2.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	var evs []map[string]any
	if err := json.NewDecoder(rec2.Body).Decode(&evs); err != nil {
		t.Fatalf("decode /events body: %v", err)
	}
	if len(evs) != 2 {
		t.Errorf("len(events) = %d, want 2", len(evs))
	}
	if evs[0]["kind"] != "pump_on" {
		t.Errorf("events[0].kind = %v, want pump_on", evs[0]["kind"])
	}
}

func TestGetMetrics(t *testing.T) {
	h, rec, _, stop := newFullH(t)
	defer stop()

	rec.Record("pump_on", "speed=100")
	rec.Record("interlock_block", "distance=15.0cm threshold=10.0cm")
	rec.Record("pump_failsafe", "after=10m0s")
	rec.Record("overtemp", "")

	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rec2.Code != http.StatusOK {
		t.Fatalf("/metrics status = %d, want 200", rec2.Code)
	}
	ct := rec2.Header().Get("Content-Type")
	if ct != "text/plain; version=0.0.4" {
		t.Errorf("Content-Type = %q, want text/plain; version=0.0.4", ct)
	}
	body := rec2.Body.String()
	for _, substr := range []string{
		"gardynd_temperature_c",
		"gardynd_pump_runs_total",
		"# HELP",
		"# TYPE",
	} {
		if !strings.Contains(body, substr) {
			t.Errorf("/metrics body missing %q\nbody:\n%s", substr, body)
		}
	}
}

// newHFull builds a HandlerFull backed by a mock core, suitable for auth tests.
func newHFull(t *testing.T) (http.Handler, func()) {
	t.Helper()
	st := state.New()
	c := core.New(mock.New(), st)
	go c.Run()
	deps := ControlDeps{
		GetSchedules:       func() config.Schedules { return config.Schedules{} },
		PutSchedule:        func(string, config.Schedule) error { return nil },
		SetScheduleEnabled: func(string, bool) error { return nil },
		SetWaterLowCM:      func(float64) error { return nil },
	}
	h := HandlerFull(c, st, mock.New(), state.NewFrames(), deps, nil, nil)
	return h, c.Stop
}

func TestWithAuthDisabledPassesThrough(t *testing.T) {
	inner, stop := newHFull(t)
	defer stop()
	h := WithAuth(inner, "") // empty token = auth disabled

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("auth disabled: /healthz status = %d, want 200", rec.Code)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/state", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("auth disabled: /state status = %d, want 200 (no token configured)", rec.Code)
	}
}

func TestWithAuthCorrectTokenAllows(t *testing.T) {
	inner, stop := newHFull(t)
	defer stop()
	h := WithAuth(inner, "mytoken")

	req := httptest.NewRequest(http.MethodGet, "/state", nil)
	req.Header.Set("Authorization", "Bearer mytoken")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("correct token: /state status = %d, want 200", rec.Code)
	}
}

func TestWithAuthMissingOrWrongTokenRejects(t *testing.T) {
	inner, stop := newHFull(t)
	defer stop()
	h := WithAuth(inner, "mytoken")

	// missing header
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/state", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("missing header: status = %d, want 401", rec.Code)
	}
	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["error"] != "unauthorized" {
		t.Errorf("error body = %v, want {\"error\":\"unauthorized\"}", body)
	}

	// wrong token
	rec = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/state", nil)
	req.Header.Set("Authorization", "Bearer wrongtoken")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("wrong token: status = %d, want 401", rec.Code)
	}
}

func TestWithAuthHealthzExempt(t *testing.T) {
	inner, stop := newHFull(t)
	defer stop()
	h := WithAuth(inner, "mytoken")

	// /healthz must be reachable without any Authorization header
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("/healthz with token configured but no header: status = %d, want 200", rec.Code)
	}
}

func TestMaxBodySizeRejectsOversizedRequest(t *testing.T) {
	inner, stop := newHFull(t)
	defer stop()

	const maxBytes = 1 << 20 // 1 MiB — must match the constant in main.go
	h := http.MaxBytesHandler(inner, maxBytes)

	// Build a single JSON object whose body exceeds 1 MiB.  The decoder must
	// read the entire object (including the large "noise" string field) before
	// it can return, so http.MaxBytesReader fires mid-read and Decode returns
	// *http.MaxBytesError, which levelHandler maps to 400.
	// A repeated stream of {"value":1} does NOT trigger the limit because
	// json.Decoder only reads the first 11-byte object and returns immediately.
	noise := strings.Repeat("A", (2 << 20))
	oversized := `{"value":1,"noise":"` + noise + `"}`
	req := httptest.NewRequest(http.MethodPost, "/light/brightness", strings.NewReader(oversized))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	// The oversized body must be rejected — 400 (decode error) or 413 (MaxBytesHandler).
	if rec.Code >= 500 {
		t.Errorf("oversized body: status = %d, want 4xx (body must be rejected, not cause 5xx)", rec.Code)
	}
	if rec.Code == http.StatusOK {
		t.Errorf("oversized body: status = 200, want 4xx (body must be rejected)")
	}
}

func TestGetHealthzRich(t *testing.T) {
	h, _, tr, stop := newFullH(t)
	defer stop()

	tr.Touch("env")
	tr.Touch("distance")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("/healthz status = %d, want 200", rec.Code)
	}
	var body struct {
		Status  string                         `json:"status"`
		UptimeS int64                          `json:"uptime_s"`
		Sensors map[string]health.SensorStatus `json:"sensors"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode /healthz body: %v", err)
	}
	if body.Status != "ok" {
		t.Errorf("status = %q, want ok", body.Status)
	}
	if _, ok := body.Sensors["env"]; !ok {
		t.Error("/healthz sensors missing 'env'")
	}
	if !body.Sensors["env"].OK {
		t.Error("/healthz sensors env.ok = false, want true (just touched)")
	}
	if _, ok := body.Sensors["distance"]; !ok {
		t.Error("/healthz sensors missing 'distance'")
	}
}

// TestSSEStreamDeliversDataFrame verifies that GET /state/stream:
//   - returns 200 with Content-Type: text/event-stream
//   - immediately sends an initial data: frame (current snapshot)
//   - sends another data: frame when the state changes
//
// Uses httptest.NewServer so the underlying TCP connection supports
// http.Flusher (unlike httptest.ResponseRecorder).
func TestSSEStreamDeliversDataFrame(t *testing.T) {
	st := state.New()
	c := core.New(mock.New(), st)
	go c.Run()
	defer c.Stop()

	deps := ControlDeps{
		GetSchedules:       func() config.Schedules { return config.Schedules{} },
		PutSchedule:        func(string, config.Schedule) error { return nil },
		SetScheduleEnabled: func(string, bool) error { return nil },
		SetWaterLowCM:      func(float64) error { return nil },
	}
	h := HandlerFull(c, st, mock.New(), state.NewFrames(), deps, nil, nil)

	srv := httptest.NewServer(h)
	defer srv.Close()

	ctx, cancelCtx := context.WithCancel(context.Background())
	defer cancelCtx()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/state/stream", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("SSE status = %d, want 200", resp.StatusCode)
	}
	ct := resp.Header.Get("Content-Type")
	if ct != "text/event-stream" {
		t.Fatalf("Content-Type = %q, want text/event-stream", ct)
	}

	// Read lines from the stream; signal when we find the first data: frame.
	found := make(chan bool, 1)
	go func() {
		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "data:") {
				found <- true
				return
			}
		}
		found <- false
	}()

	// Trigger a state change to guarantee a notification arrives.
	time.Sleep(20 * time.Millisecond) // let handler send the initial frame first
	st.SetLight(true, 99)

	select {
	case ok := <-found:
		if !ok {
			t.Fatal("SSE stream closed without any data: line")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no data: frame received within 2s")
	}
	cancelCtx()
}
