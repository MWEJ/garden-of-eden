package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/iot-root/garden-of-eden/internal/core"
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
