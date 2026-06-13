package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/iot-root/garden-of-eden/internal/config"
	"github.com/iot-root/garden-of-eden/internal/core"
	"github.com/iot-root/garden-of-eden/internal/httpapi"
	"github.com/iot-root/garden-of-eden/internal/hw"
	"github.com/iot-root/garden-of-eden/internal/hw/mock"
	"github.com/iot-root/garden-of-eden/internal/hw/real"
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
	c := core.New(devs, st)
	go c.Run()
	defer c.Stop()

	addr := fmt.Sprintf(":%d", cfg.HTTP.Port)
	server := &http.Server{Addr: addr, Handler: httpapi.HandlerWithSensors(c, st, devs)}
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
