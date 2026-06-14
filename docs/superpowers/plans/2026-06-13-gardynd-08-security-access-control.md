# gardynd Plan 8 — Security & Access Control Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Harden the gardynd HTTP surface with bearer-token authentication, a configurable bind address, and a request body size cap — all backward-compatible and wired via config/env with zero breaking changes to existing behaviour.

**Architecture:** Three surgical, disjoint config additions (`AuthToken`, `BindAddress` on `HTTPConfig`) are wired in `main.go` alongside two new layered handlers: `WithAuth` (constant-time token check, `/healthz` exempt) and `http.MaxBytesHandler` (1 MiB cap). The middleware wraps `HandlerFull(...)` in main; httpapi internals are untouched. No new packages — only `crypto/subtle`, `net`, and `strconv` from the stdlib.

**Tech Stack:** Go stdlib (`crypto/subtle`, `net`, `strconv`, `net/http`). No new module dependencies.

**Spec:** `docs/superpowers/specs/2026-06-12-gardynd-go-service-design.md`
**Depends on:** Plan 1 (core, state store, httpapi, config), Plan 3 (HandlerFull, ControlDeps), Plan 6 (error bodies normalized to `{"error": ...}`).

**HA integration note (cross-branch, do NOT edit those files):** The `ha-integration` branch contains `GardyndClient` and the config flow. Once this plan lands on `main`, that branch will need two follow-ups: (1) add an `auth_token` field to the HA config schema and pass it as `Authorization: Bearer <token>` on every `GardyndClient` request; (2) expose the token in the HA config-flow UI. Neither change is in scope here — this plan only modifies `gardynd`.

---

## File Structure (this plan)

```
internal/config/config.go         MODIFY: add AuthToken + BindAddress to HTTPConfig, defaults, applyEnv
internal/config/config_test.go    MODIFY: append AuthToken + BindAddress default/env tests
internal/httpapi/httpapi.go       MODIFY: add WithAuth middleware (func WithAuth(h http.Handler, token string) http.Handler)
internal/httpapi/httpapi_test.go  MODIFY: append four WithAuth tests + one body-size test
cmd/gardynd/main.go               MODIFY: net.JoinHostPort bind, wrap HandlerFull with WithAuth + MaxBytesHandler
```

---

### Task 1: Config — `AuthToken` + `BindAddress` fields

**Depends on:** none (within this plan; touches only `internal/config/`)

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`

Add `AuthToken string` (yaml `auth_token`, env `HTTP_AUTH_TOKEN`, default `""`) and `BindAddress string` (yaml `bind_address`, env `HTTP_BIND_ADDRESS`, default `""`) to `HTTPConfig`. Empty defaults are backward-compatible: auth is disabled when `AuthToken == ""`, and `BindAddress == ""` means listen on all interfaces (reproduces the current `":5000"` behaviour when combined with `net.JoinHostPort`).

- [ ] **Step 1: Write the failing tests (append to config_test.go)**

Append to `internal/config/config_test.go`:
```go
func TestHTTPAuthTokenDefault(t *testing.T) {
	c, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if c.HTTP.AuthToken != "" {
		t.Errorf("HTTP.AuthToken default = %q, want empty string (auth disabled)", c.HTTP.AuthToken)
	}
}

func TestHTTPAuthTokenEnvOverride(t *testing.T) {
	t.Setenv("HTTP_AUTH_TOKEN", "supersecret")
	c, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if c.HTTP.AuthToken != "supersecret" {
		t.Errorf("HTTP.AuthToken = %q, want %q", c.HTTP.AuthToken, "supersecret")
	}
}

func TestHTTPBindAddressDefault(t *testing.T) {
	c, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if c.HTTP.BindAddress != "" {
		t.Errorf("HTTP.BindAddress default = %q, want empty string (all interfaces)", c.HTTP.BindAddress)
	}
}

func TestHTTPBindAddressEnvOverride(t *testing.T) {
	t.Setenv("HTTP_BIND_ADDRESS", "127.0.0.1")
	c, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if c.HTTP.BindAddress != "127.0.0.1" {
		t.Errorf("HTTP.BindAddress = %q, want %q", c.HTTP.BindAddress, "127.0.0.1")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config/ -run 'TestHTTPAuthToken|TestHTTPBindAddress' -v`
Expected: build failure — `c.HTTP.AuthToken undefined` / `c.HTTP.BindAddress undefined`.

- [ ] **Step 3: Add the two fields, defaults, and env hooks**

In `internal/config/config.go`, replace the existing `HTTPConfig` struct:
```go
type HTTPConfig struct {
	Port        int    `yaml:"port"`
	BindAddress string `yaml:"bind_address"`
	AuthToken   string `yaml:"auth_token"`
}
```

The `defaults()` function needs no change — zero values for the two new string fields are already the correct defaults (`""` = all interfaces, `""` = auth disabled).

In `applyEnv`, add two calls alongside the existing `envInt(&c.HTTP.Port, "HTTP_PORT")` line:
```go
func applyEnv(c *Config) {
	envInt(&c.HTTP.Port, "HTTP_PORT")
	envStr(&c.HTTP.BindAddress, "HTTP_BIND_ADDRESS")
	envStr(&c.HTTP.AuthToken, "HTTP_AUTH_TOKEN")
	envStr(&c.Device.Identifier, "MQTT_IDENTIFIER")
	envStr(&c.Device.Model, "MQTT_DEVICE_MODEL")
	envStr(&c.Device.Version, "MQTT_VERSION")
	envStr(&c.Camera.UpperDevice, "UPPER_CAMERA_DEVICE")
	envStr(&c.Camera.LowerDevice, "LOWER_CAMERA_DEVICE")
	envStr(&c.Camera.Resolution, "CAMERA_RESOLUTION")
	envInt(&c.Camera.IntervalSeconds, "IMAGE_INTERVAL_SECONDS")
	envStr(&c.SensorType, "SENSOR_TYPE")
	envFloat(&c.Water.LowCM, "WATER_LOW_CM")
	envInt(&c.TelemetryIntervalSeconds, "TELEMETRY_INTERVAL_SECONDS")
}
```

> Note: `envStr` skips the assignment when the env var is absent or empty (`if v != "" { *dst = v }`), so `HTTP_AUTH_TOKEN=""` leaves `AuthToken` at its default empty string — correct. Setting `HTTP_AUTH_TOKEN` to a non-empty value enables auth. `AuthToken` is intentionally not written back to disk by `LoadFileOnly`/`Save` during runtime API edits (schedules, water threshold) because it is a runtime-only credential, not a schedule-like persistent setting. A YAML `auth_token:` line in the config file is still honoured at startup via `yaml.Unmarshal`.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/config/ -run 'TestHTTPAuthToken|TestHTTPBindAddress' -v`
Expected:
```
--- PASS: TestHTTPAuthTokenDefault (0.00s)
--- PASS: TestHTTPAuthTokenEnvOverride (0.00s)
--- PASS: TestHTTPBindAddressDefault (0.00s)
--- PASS: TestHTTPBindAddressEnvOverride (0.00s)
PASS
```

- [ ] **Step 5: Run full config suite to confirm no regressions**

Run: `go test ./internal/config/ -v`
Expected: all existing tests PASS plus the four new ones.

---

### Task 2: httpapi — `WithAuth` middleware

**Depends on:** none (within this plan; touches only `internal/httpapi/`; the new `HTTPConfig` fields from Task 1 are NOT imported here — `WithAuth` takes a plain `string`, keeping httpapi decoupled from config)

**Files:**
- Modify: `internal/httpapi/httpapi.go`
- Modify: `internal/httpapi/httpapi_test.go`

Implement `func WithAuth(h http.Handler, token string) http.Handler`. When `token == ""` the wrapper is a pass-through (auth disabled). When `token != ""` every request must carry `Authorization: Bearer <token>`; comparison uses `crypto/subtle.ConstantTimeCompare` to prevent timing attacks; mismatches return `401 {"error":"unauthorized"}`. The `/healthz` path is unconditionally exempt — it must remain reachable by health checks even when a token is configured. `/metrics` is not yet present; note it as a future exemption candidate when added in a later plan.

- [ ] **Step 1: Write the failing tests (append to httpapi_test.go)**

Append to `internal/httpapi/httpapi_test.go`:
```go
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
	h := HandlerFull(c, st, mock.New(), state.NewFrames(), deps)
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/httpapi/ -run 'TestWithAuth' -v`
Expected: build failure — `undefined: WithAuth`.

- [ ] **Step 3: Implement WithAuth in httpapi.go**

Add the following to `internal/httpapi/httpapi.go`. Add `"crypto/subtle"` and `"strings"` to the import block (alongside the existing `"encoding/json"`, `"fmt"`, `"net/http"`):

```go
import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/iot-root/garden-of-eden/internal/config"
	"github.com/iot-root/garden-of-eden/internal/core"
	"github.com/iot-root/garden-of-eden/internal/hw"
	"github.com/iot-root/garden-of-eden/internal/state"
)
```

Add the `WithAuth` function at the bottom of the file (after `HandlerFull`):

```go
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
```

- [ ] **Step 4: Run new tests to verify they pass**

Run: `go test ./internal/httpapi/ -run 'TestWithAuth' -v`
Expected:
```
--- PASS: TestWithAuthDisabledPassesThrough (0.00s)
--- PASS: TestWithAuthCorrectTokenAllows (0.00s)
--- PASS: TestWithAuthMissingOrWrongTokenRejects (0.00s)
--- PASS: TestWithAuthHealthzExempt (0.00s)
PASS
```

- [ ] **Step 5: Run full httpapi suite to confirm no regressions**

Run: `go test ./internal/httpapi/ -v`
Expected: all existing tests PASS plus the four new ones.

---

### Task 3: Request body size limit

**Depends on:** Task 2 (uses `newHFull` helper defined there; the test is appended to httpapi_test.go which Task 2 already modified)

**Files:**
- Modify: `internal/httpapi/httpapi_test.go`

`http.MaxBytesHandler` is a stdlib function — no new httpapi.go code is needed. The wiring lives in `main.go` (Task 4). This task proves the limit works by wrapping the handler in a test, so the behaviour is locked before the main.go change is written.

The cap is 1 MiB (`1 << 20` bytes). A POST body exceeding the limit causes `json.Decoder.Decode` to return `*http.MaxBytesError`; `levelHandler` already returns 400 on any decode error, so oversized bodies to POST routes return 400. The test asserts the body is rejected (status ≤ 499, specifically 400 in our case via the existing decode-error path).

> Design note: `http.MaxBytesHandler` sets `r.Body = http.MaxBytesReader(w, r.Body, max)`. When the client sends more than `max` bytes, the read returns an error on the first `Read` call that crosses the limit. `json.NewDecoder(r.Body).Decode(...)` hits this and returns an error, which `levelHandler` maps to `400 {"error":"invalid JSON body"}`. If Go's `http.Server` is used (real traffic), it also closes the connection and writes 413 automatically — but under `httptest`, only the 400 path fires. The test asserts status < 500 and that the body was not parsed successfully.

- [ ] **Step 1: Write the failing test (append to httpapi_test.go)**

Append to `internal/httpapi/httpapi_test.go`:
```go
func TestMaxBodySizeRejectsOversizedRequest(t *testing.T) {
	inner, stop := newHFull(t)
	defer stop()

	const maxBytes = 1 << 20 // 1 MiB — must match the constant in main.go
	h := http.MaxBytesHandler(inner, maxBytes)

	// Build a body that is 2 MiB of JSON-looking noise (definitely > 1 MiB).
	oversized := strings.Repeat(`{"value":1}`, (2<<20)/len(`{"value":1}`)+1)
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/httpapi/ -run 'TestMaxBodySize' -v`
Expected: test fails — without `http.MaxBytesHandler` wrapping the handler the large body is accepted and decoded as a stream returning many JSON values; the first valid `{"value":1}` object is parsed and returns 200.

> Exact failure output: `oversized body: status = 200, want 4xx (body must be rejected)` or the test panics due to OOM from reading 2 MiB. Either confirms the guard is not yet in place.

- [ ] **Step 3: Confirm the test passes when MaxBytesHandler is applied**

The test itself applies `http.MaxBytesHandler` (`h := http.MaxBytesHandler(inner, maxBytes)`), so Step 1's test is already self-contained — it does not rely on main.go wiring. Once the test file has been appended, the test must pass immediately because the wrapping is inline in the test.

Run: `go test ./internal/httpapi/ -run 'TestMaxBodySize' -v`
Expected:
```
--- PASS: TestMaxBodySizeRejectsOversizedRequest (0.00s)
PASS
```

- [ ] **Step 4: Run full httpapi suite to confirm no regressions**

Run: `go test ./internal/httpapi/ -v`
Expected: all existing tests PASS plus the new one.

---

### Task 4: Wire everything in main.go

**Depends on:** Task 1 (HTTPConfig.BindAddress + HTTPConfig.AuthToken), Task 2 (httpapi.WithAuth)

**Files:**
- Modify: `cmd/gardynd/main.go`

Replace the `addr := fmt.Sprintf(":%d", cfg.HTTP.Port)` line with `net.JoinHostPort`, then wrap `HandlerFull(...)` with `httpapi.WithAuth` and `http.MaxBytesHandler`. Add `"net"` and `"strconv"` to the import block (replacing `"fmt"` which is no longer needed for address construction — keep `"fmt"` if it is used elsewhere in main; check before removing).

- [ ] **Step 1: Write the failing build check**

The changes to main.go are not independently testable with `go test`, but they must compile. Verify the current state compiles before editing:

Run: `go build ./cmd/gardynd/`
Expected: exits 0 (baseline build passes before any edits).

- [ ] **Step 2: Update the import block in main.go**

In `cmd/gardynd/main.go`, add `"net"` and `"strconv"` to the import block. The existing `"fmt"` is used in the `log.Printf("REST listening on %s", addr)` call, so keep it. The updated import block:

```go
import (
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/iot-root/garden-of-eden/internal/config"
	"github.com/iot-root/garden-of-eden/internal/core"
	"github.com/iot-root/garden-of-eden/internal/discovery"
	"github.com/iot-root/garden-of-eden/internal/httpapi"
	"github.com/iot-root/garden-of-eden/internal/hw"
	"github.com/iot-root/garden-of-eden/internal/hw/mock"
	"github.com/iot-root/garden-of-eden/internal/hw/real"
	"github.com/iot-root/garden-of-eden/internal/publish"
	"github.com/iot-root/garden-of-eden/internal/state"
)
```

- [ ] **Step 3: Replace the server construction block**

Locate the existing lines in `cmd/gardynd/main.go`:
```go
	addr := fmt.Sprintf(":%d", cfg.HTTP.Port)
	server := &http.Server{Addr: addr, Handler: httpapi.HandlerFull(c, st, devs, frames, deps)}
```

Replace them with:
```go
	const maxRequestBytes = 1 << 20 // 1 MiB body cap

	addr := net.JoinHostPort(cfg.HTTP.BindAddress, strconv.Itoa(cfg.HTTP.Port))
	handler := httpapi.HandlerFull(c, st, devs, frames, deps)
	handler = httpapi.WithAuth(handler, cfg.HTTP.AuthToken)
	handler = http.MaxBytesHandler(handler, maxRequestBytes)
	server := &http.Server{Addr: addr, Handler: handler}
```

> Layering order matters: `MaxBytesHandler` is the outermost wrapper — it caps body reads before auth runs. `WithAuth` is next — it checks the token before the inner mux processes the request. `HandlerFull` is the innermost handler. This means: an oversized body from an unauthenticated client is still rejected at the body-size layer (MaxBytes fires first), which avoids even spending time on token comparison for junk requests.

> The existing `--http-port` flag override (`if *httpPort != 0 { cfg.HTTP.Port = *httpPort }`) runs before this block and still takes precedence over `cfg.HTTP.Port`, so the flag override remains fully functional.

> When `cfg.HTTP.BindAddress == ""`, `net.JoinHostPort("", "5000")` returns `":5000"` — identical to the prior `fmt.Sprintf(":%d", cfg.HTTP.Port)` output. No behaviour change for existing deployments.

- [ ] **Step 4: Build both targets**

Run: `go build ./cmd/gardynd/ && GOARCH=arm GOARM=7 GOOS=linux go build -o /dev/null ./cmd/gardynd/`
Expected: both build with no errors.

> If a `Makefile` target exists for Pi builds (`make build-pi`), use `make build && make build-pi` instead.

---

### Task 5: Full suite + single commit

**Depends on:** Tasks 1–4

**Files:** (no new edits — validation only)

- [ ] **Step 1: Run the full test suite with race detector**

Run: `go test ./... -race`
Expected: all packages PASS, no race conditions detected.

- [ ] **Step 2: Smoke-test the mock binary (no token)**

Run `./bin/gardynd --hw=mock` (or `go run ./cmd/gardynd/ --hw=mock`) in one terminal, then in another:

```
curl -s http://localhost:5000/healthz
curl -s http://localhost:5000/state
```

Both must return 200 — auth is disabled when `HTTP_AUTH_TOKEN` is not set.

- [ ] **Step 3: Smoke-test with token set**

Kill the previous process. Start with:
```
HTTP_AUTH_TOKEN=testtoken go run ./cmd/gardynd/ --hw=mock
```

Then:
```
# should return 401
curl -s -o /dev/null -w "%{http_code}" http://localhost:5000/state

# should return 200
curl -s -o /dev/null -w "%{http_code}" \
  -H "Authorization: Bearer testtoken" http://localhost:5000/state

# /healthz must return 200 without any header
curl -s -o /dev/null -w "%{http_code}" http://localhost:5000/healthz
```

Expected output: `401`, `200`, `200` on successive lines.

- [ ] **Step 4: Smoke-test bind address**

Kill the previous process. Start with:
```
HTTP_BIND_ADDRESS=127.0.0.1 go run ./cmd/gardynd/ --hw=mock
```

Confirm the server logs `REST listening on 127.0.0.1:5000` and that `curl http://localhost:5000/healthz` succeeds (loopback is within 127.0.0.1 scope on Linux).

- [ ] **Step 5: Commit (single commit covering all changes)**

```
git add internal/config/config.go internal/config/config_test.go \
        internal/httpapi/httpapi.go internal/httpapi/httpapi_test.go \
        cmd/gardynd/main.go
git commit -m "feat: bearer-token auth, configurable bind address, request body size cap"
```

---

## Self-Review

**1. Spec coverage**

| Requirement | Task |
|---|---|
| `AuthToken string` on `HTTPConfig`, yaml `auth_token`, default `""` | Task 1 |
| `HTTP_AUTH_TOKEN` env override | Task 1 |
| Auth disabled when `AuthToken == ""` (backward-compatible) | Task 2 (`WithAuth` returns `h` unchanged when `token == ""`) |
| `WithAuth(h http.Handler, token string) http.Handler` public constructor | Task 2 |
| `crypto/subtle.ConstantTimeCompare` for token comparison | Task 2 |
| 401 `{"error":"unauthorized"}` on missing/wrong token | Task 2 |
| `/healthz` exempt from auth even when token is set | Task 2 |
| `/metrics` future exemption noted | Task 2 (`WithAuth` comment) |
| `BindAddress string` on `HTTPConfig`, yaml `bind_address`, default `""` | Task 1 |
| `HTTP_BIND_ADDRESS` env override | Task 1 |
| `net.JoinHostPort` in main (`""` → `:5000`, `"127.0.0.1"` → `127.0.0.1:5000`) | Task 4 |
| `--http-port` flag still overrides port | Task 4 (override runs before JoinHostPort; unchanged) |
| `http.MaxBytesHandler` at 1 MiB cap applied in main | Task 4 |
| Oversized body to POST route is rejected | Task 3 + Task 4 |
| HA integration follow-up noted (cross-branch, not edited here) | Plan header |
| Single commit | Task 5, Step 5 |

No gaps identified.

**2. Placeholder scan**

No "TBD", "TODO", "implement later", or "similar to Task N" text. Every step contains complete Go code or an exact shell command with expected output. The `/metrics` mention is a concrete forward pointer ("when /metrics is added in a later plan, add it to the exempt list here") — not a deferred implementation.

**3. Type consistency**

- `HTTPConfig.AuthToken string` defined in Task 1, read as `cfg.HTTP.AuthToken` (string) in Task 4's `httpapi.WithAuth(handler, cfg.HTTP.AuthToken)` — consistent.
- `HTTPConfig.BindAddress string` defined in Task 1, read as `cfg.HTTP.BindAddress` (string) in Task 4's `net.JoinHostPort(cfg.HTTP.BindAddress, strconv.Itoa(cfg.HTTP.Port))` — consistent; `strconv.Itoa` correctly converts `int` Port to `string`.
- `func WithAuth(h http.Handler, token string) http.Handler` defined in Task 2, called in Task 4 as `httpapi.WithAuth(handler, cfg.HTTP.AuthToken)` — signature matches.
- `writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})` in `WithAuth` uses the same private helper already present in `httpapi.go` — no new dependency.
- `http.MaxBytesHandler(handler, maxRequestBytes)` — stdlib call; `maxRequestBytes` is `int64` (the const `1 << 20` is untyped, assignable to `int64`). `http.MaxBytesHandler` signature: `func MaxBytesHandler(h Handler, n int64) Handler` — compatible.
- `newHFull` helper defined in Task 2's test additions; Task 3 appends a test to the same file that calls `newHFull` — both in `package httpapi`, no import needed.

All types, method signatures, and field names are consistent across tasks and with the existing source.

**4. Dependency audit**

- **Task 1** (`internal/config/`): Only task that touches config files. No other task in this plan modifies `config.go` or `config_test.go`. Marked `none` — correct.
- **Task 2** (`internal/httpapi/httpapi.go` + `httpapi_test.go`): Does not import or reference the new `HTTPConfig` fields from Task 1 — `WithAuth` takes a plain `string`. The `newHFull` helper it introduces is used by Task 3, but Task 3 only appends to `httpapi_test.go` (same file Task 2 already modifies). Marked `none` — correct (the `newHFull` dependency is within the same file; the helper is defined first).
- **Task 3** (`internal/httpapi/httpapi_test.go` only): Appends one test that calls `newHFull` (defined by Task 2's test additions) and `http.MaxBytesHandler` (stdlib). Depends on Task 2 having been applied so `newHFull` exists. Marked `Depends on: Task 2` — correct.
- **Task 4** (`cmd/gardynd/main.go`): Reads `cfg.HTTP.BindAddress` and `cfg.HTTP.AuthToken` (Task 1) and calls `httpapi.WithAuth` (Task 2). Depends on both. Marked `Depends on: Task 1, Task 2` — correct. Task 3 provides the test proof for body-size limiting but Task 4 does not depend on Task 3 to compile or run — however, running Task 3 first ensures the behaviour is verified before wiring. Ordering Task 3 before Task 4 is recommended but not a hard dependency.
- **Task 5** (validation + commit): Depends on Tasks 1–4 all being applied. Marked `Depends on: Tasks 1–4` — correct.
- Tasks 1 and 2 are mutually independent (disjoint file sets, no shared interfaces introduced in this plan) and can be executed in parallel.
