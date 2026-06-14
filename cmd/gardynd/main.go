package main

import (
	"context"
	"flag"
	"log"
	"log/slog"
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
	evtring "github.com/iot-root/garden-of-eden/internal/events"
	"github.com/iot-root/garden-of-eden/internal/health"
	"github.com/iot-root/garden-of-eden/internal/httpapi"
	"github.com/iot-root/garden-of-eden/internal/hw"
	"github.com/iot-root/garden-of-eden/internal/hw/mock"
	"github.com/iot-root/garden-of-eden/internal/hw/real"
	"github.com/iot-root/garden-of-eden/internal/publish"
	"github.com/iot-root/garden-of-eden/internal/state"
)

func main() {
	configPath := flag.String("config", "", "path to YAML config file")
	hwMode := flag.String("hw", "real", "hardware backend: real|mock")
	httpPort := flag.Int("http-port", 0, "override HTTP port (0 = use config)")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	if *httpPort != 0 {
		cfg.HTTP.Port = *httpPort
	}

	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: config.ParseLogLevel(cfg.LogLevel),
	})))

	// File-only baseline for persistence: runtime env/flag overrides live in
	// cfg, but only file-originated values (plus API edits) are written back.
	fileCfg, err := config.LoadFileOnly(*configPath)
	if err != nil {
		log.Fatalf("config (file baseline): %v", err)
	}

	var devs hw.Devices
	switch *hwMode {
	case "mock":
		devs = mock.New()
	case "real":
		d, cleanup, herr := real.New(cfg)
		if herr != nil {
			log.Fatalf("hardware init: %v", herr)
		}
		devs = d
		defer cleanup()
	default:
		log.Fatalf("unknown --hw value %q", *hwMode)
	}

	st := state.New()
	st.SetDeviceInfo(cfg.Device.Identifier, cfg.Device.Model, cfg.Device.Version)
	c := core.New(devs, st)

	rec := evtring.NewRecorder(100)
	c.SetEvents(rec)

	go c.Run()

	// Core runtime config (setters are mutex-guarded, safe post-Run).
	c.SetWaterLowCM(cfg.Water.LowCM)
	c.SetPumpMaxRuntime(time.Duration(cfg.Pump.MaxRuntimeSeconds) * time.Second)
	c.SetCutLightOnOverTemp(cfg.OverTemp.CutLight)
	c.SetBlockOnSensorError(cfg.Water.BlockOnSensorError)
	c.SetPumpStateFile(cfg.Pump.StateFile)

	// Restart-enforced failsafe: if a previous run crashed while the pump was on,
	// enforce the remaining max-runtime (or force off if already expired).
	maxRT := time.Duration(cfg.Pump.MaxRuntimeSeconds) * time.Second
	if remaining := c.EnforcePumpRuntime(cfg.Pump.StateFile, maxRT, time.Now()); remaining > 0 {
		slog.Info("pump was running before restart; arming failsafe for remaining", "remaining", remaining)
		time.AfterFunc(remaining, func() {
			c.Submit(core.Command{Target: core.TargetPump, Action: core.ActionOff})
		})
	}

	// Schedules: live copy guarded by schedMu; persisted atomically to the
	// config file (when one was supplied).
	var schedMu sync.Mutex
	schedules := fileCfg.Schedules
	st.SetScheduleEnabled("light", schedules.Light.Enabled)
	st.SetScheduleEnabled("pump", schedules.Pump.Enabled)

	getSchedules := func() config.Schedules {
		schedMu.Lock()
		defer schedMu.Unlock()
		return schedules
	}
	persist := func() error {
		if *configPath == "" {
			return nil
		}
		schedMu.Lock()
		fileCfg.Schedules = schedules
		snapshot := fileCfg
		schedMu.Unlock()
		return snapshot.Save(*configPath)
	}
	putSchedule := func(ch string, s config.Schedule) error {
		schedMu.Lock()
		if ch == "light" {
			schedules.Light = s
		} else {
			schedules.Pump = s
		}
		schedMu.Unlock()
		st.SetScheduleEnabled(ch, s.Enabled)
		return persist()
	}
	setSchedEnabled := func(ch string, on bool) error {
		schedMu.Lock()
		if ch == "light" {
			schedules.Light.Enabled = on
		} else {
			schedules.Pump.Enabled = on
		}
		schedMu.Unlock()
		return persist()
	}
	setWaterCM := func(cm float64) error {
		c.SetWaterLowCM(cm)
		schedMu.Lock()
		fileCfg.Water.LowCM = cm
		schedMu.Unlock()
		return persist()
	}

	if cfg.HasSolarEntry() && cfg.Location.Latitude == 0 && cfg.Location.Longitude == 0 {
		slog.Warn("solar schedule entries present but location lat/lon unset; " +
			"solar times will be computed at (0,0). Set LAT/LON or config location.")
	}
	sl := config.SchedLocation{
		Loc: cfg.Zone(),
		Lat: cfg.Location.Latitude,
		Lon: cfg.Location.Longitude,
	}
	sched := core.NewSchedulerLoc(c, getSchedules, sl, config.NOAASun{})
	go sched.Run()

	// Over-temp monitor: the publisher reads PCBTemp.OverTemp() each cycle and
	// forwards it here, which submits to the single-writer core.
	onOverTemp := func(over bool) {
		v := 0
		if over {
			v = 1
		}
		c.Submit(core.Command{Target: core.TargetOverTemp, Action: core.ActionOn, Value: v})
	}

	frames := state.NewFrames()
	pub := publish.New(devs, st, frames, time.Duration(cfg.TelemetryIntervalSeconds)*time.Second, onOverTemp)

	tr := health.NewTracker()
	pub.SetHealthTracker(tr)

	go pub.Run()
	go pub.RunCameras(time.Duration(cfg.Camera.IntervalSeconds) * time.Second)

	buttonDone := make(chan struct{})
	if devs.Button != nil {
		events := devs.Button.Events()
		go func() {
			for {
				select {
				case <-buttonDone:
					return
				case ev, ok := <-events:
					if !ok {
						return
					}
					switch ev {
					case hw.SinglePress:
						c.Submit(core.Command{Target: core.TargetLight, Action: core.ActionOn})
					case hw.DoublePress:
						c.Submit(core.Command{Target: core.TargetPump, Action: core.ActionOn})
					}
				}
			}
		}()
	}

	stop := discovery.Advertise("gardynd-"+cfg.Device.Identifier, cfg.HTTP.Port)

	deps := httpapi.ControlDeps{
		GetSchedules:       getSchedules,
		PutSchedule:        putSchedule,
		SetScheduleEnabled: setSchedEnabled,
		SetWaterLowCM:      setWaterCM,
	}

	const maxRequestBytes = 1 << 20 // 1 MiB body cap

	addr := net.JoinHostPort(cfg.HTTP.BindAddress, strconv.Itoa(cfg.HTTP.Port))
	handler := httpapi.HandlerFull(c, st, devs, frames, deps, rec, tr)
	handler = httpapi.WithAuth(handler, cfg.HTTP.AuthToken)
	handler = http.MaxBytesHandler(handler, maxRequestBytes)
	server := &http.Server{Addr: addr, Handler: handler}
	go func() {
		slog.Info("REST listening", "addr", addr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("http server failed", "err", err)
		}
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	slog.Info("shutting down")

	// 1. Stop accepting new HTTP requests and drain in-flight ones, so no late
	//    request can turn the pump back on after we drive it off.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		slog.Error("http shutdown", "err", err)
	}

	// 2. Stop the button goroutine so it cannot submit a pump-on after this.
	close(buttonDone)

	// 3. Stop the scheduler so a minute-boundary tick cannot re-drive the pump.
	sched.Stop()

	// 4. Drive the pump OFF and wait for the core to apply it. PWM hardware holds
	//    its last duty cycle across process exit, so a clean exit MUST turn the
	//    pump off explicitly. This also clears the persisted pump-state file via
	//    applyPump's ActionOff path.
	if !submitAndWaitPumpOff(c, st, 3*time.Second) {
		slog.Warn("pump did not confirm OFF within timeout")
	}

	// 5. Stop the publishers and core, then withdraw discovery.
	pub.Stop()
	c.Stop()
	stop()
}

// submitAndWaitPumpOff submits a pump-OFF command and waits until the snapshot
// confirms the pump is off (or the timeout elapses). Reuses the single-writer
// command channel rather than touching the device directly, so the off goes
// through the same applyPump path that disarms the failsafe and clears the
// persisted pump-state file. Returns true if confirmed off.
func submitAndWaitPumpOff(c *core.Core, st *state.Store, timeout time.Duration) bool {
	c.Submit(core.Command{Target: core.TargetPump, Action: core.ActionOff})
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !st.Snapshot().Pump.On {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return !st.Snapshot().Pump.On
}
