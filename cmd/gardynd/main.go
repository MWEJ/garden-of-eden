package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
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
	go c.Run()
	defer c.Stop()

	// Core runtime config (setters are mutex-guarded, safe post-Run).
	c.SetWaterLowCM(cfg.Water.LowCM)
	c.SetPumpMaxRuntime(10 * time.Minute)
	c.SetCutLightOnOverTemp(cfg.OverTemp.CutLight)

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

	sched := core.NewScheduler(c, getSchedules)
	go sched.Run()
	defer sched.Stop()

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
	go pub.Run()
	go pub.RunCameras(time.Duration(cfg.Camera.IntervalSeconds) * time.Second)
	defer pub.Stop()

	if devs.Button != nil {
		go func() {
			for ev := range devs.Button.Events() {
				switch ev {
				case hw.SinglePress:
					c.Submit(core.Command{Target: core.TargetLight, Action: core.ActionOn})
				case hw.DoublePress:
					c.Submit(core.Command{Target: core.TargetPump, Action: core.ActionOn})
				}
			}
		}()
	}

	stop := discovery.Advertise("gardynd-"+cfg.Device.Identifier, cfg.HTTP.Port)
	defer stop()

	deps := httpapi.ControlDeps{
		GetSchedules:       getSchedules,
		PutSchedule:        putSchedule,
		SetScheduleEnabled: setSchedEnabled,
		SetWaterLowCM:      setWaterCM,
	}

	addr := fmt.Sprintf(":%d", cfg.HTTP.Port)
	server := &http.Server{Addr: addr, Handler: httpapi.HandlerFull(c, st, devs, frames, deps)}
	go func() {
		log.Printf("REST listening on %s", addr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("http: %v", err)
		}
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	log.Printf("shutting down")
}
