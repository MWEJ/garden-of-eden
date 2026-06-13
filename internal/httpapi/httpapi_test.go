package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/iot-root/garden-of-eden/internal/config"
	"github.com/iot-root/garden-of-eden/internal/core"
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
	h := HandlerFull(c, st, mock.New(), state.NewFrames(), deps)

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
	h := HandlerFull(c, st, mock.New(), state.NewFrames(), deps)
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
	h := HandlerFull(c, st, mock.New(), state.NewFrames(), deps)
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
	h := HandlerFull(c, st, mock.New(), state.NewFrames(), deps)
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
	h := HandlerFull(c, st, mock.New(), frames, deps)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/camera/upper.jpg", nil))
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "image/jpeg" {
		t.Errorf("camera endpoint: code=%d ct=%s", rec.Code, rec.Header().Get("Content-Type"))
	}
}
