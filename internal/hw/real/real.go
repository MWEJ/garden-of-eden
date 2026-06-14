package real

import (
	"log/slog"
	"time"

	"github.com/iot-root/garden-of-eden/internal/config"
	"github.com/iot-root/garden-of-eden/internal/hw"
)

const gpioChip = "gpiochip0"

// New constructs real hardware. Light and pump are required (error if they
// fail). Sensors degrade gracefully to nil with a logged warning. The returned
// cleanup func closes every resource that owns one, in reverse order.
func New(cfg config.Config) (hw.Devices, func(), error) {
	var d hw.Devices
	var closers []func()

	light, err := NewLightPWM()
	if err != nil {
		return d, nil, err
	}
	d.Light = light

	pump, err := NewPumpPWM(gpioChip, 24, 50)
	if err != nil {
		return d, nil, err
	}
	d.Pump = pump
	closers = append(closers, func() { _ = pump.Close() })

	// Distance is always constructed (NewHCSR04 is infallible); it fails at
	// read time if the GPIO chip is unavailable.
	d.Distance = NewHCSR04(gpioChip, 26, 19)

	bus, err := OpenBus()
	if err != nil {
		slog.Warn("i2c bus unavailable, sensors disabled", "err", err)
	} else {
		closers = append(closers, func() { _ = bus.Close() })
		switch cfg.SensorType {
		case "DHT20":
			d.Env = NewEnvAHT20(bus)
		default:
			d.Env = NewEnvAM2320(bus)
		}
		if pcb, err := NewPCT2075(bus, gpioChip, 25); err != nil {
			slog.Warn("pct2075 init failed", "err", err)
		} else {
			d.PCBTemp = pcb
			closers = append(closers, func() { _ = pcb.Close() })
		}
		d.Power = NewINA219(bus)
	}

	if cam, err := NewV4L2Camera(cfg.Camera.UpperDevice, cfg.Camera.Resolution); err != nil {
		slog.Warn("upper camera init failed", "device", cfg.Camera.UpperDevice, "resolution", cfg.Camera.Resolution, "err", err)
	} else {
		d.UpperCamera = cam
	}
	if cam, err := NewV4L2Camera(cfg.Camera.LowerDevice, cfg.Camera.Resolution); err != nil {
		slog.Warn("lower camera init failed", "device", cfg.Camera.LowerDevice, "resolution", cfg.Camera.Resolution, "err", err)
	} else {
		d.LowerCamera = cam
	}

	if btn, err := NewGPIOButton(gpioChip, 13, 500*time.Millisecond, 200*time.Millisecond); err != nil {
		slog.Warn("button init failed", "err", err)
	} else {
		d.Button = btn
		closers = append(closers, func() { _ = btn.Close() })
	}

	cleanup := func() {
		for i := len(closers) - 1; i >= 0; i-- {
			closers[i]()
		}
	}
	return d, cleanup, nil
}
