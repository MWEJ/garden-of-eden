// Package hw defines hardware driver interfaces. Real implementations land in
// Plan 2; an in-memory mock lives under hw/mock.
package hw

// Light is a dimmable PWM light, brightness 0..100 percent.
type Light interface {
	SetBrightness(pct int) error
	Brightness() int
	Off() error
}

// Pump is a PWM pump, speed 0..100 percent.
type Pump interface {
	SetSpeed(pct int) error
	Speed() int
	Off() error
}

// Devices bundles the hardware the core controls. Later plans add sensor fields.
type Devices struct {
	Light Light
	Pump  Pump
}
