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

// DistanceSensor measures water-tank distance in centimeters.
type DistanceSensor interface {
	MeasureCM() (float64, error)
}

// EnvSensor reads ambient temperature (°C) and relative humidity (%).
type EnvSensor interface {
	Read() (tempC float64, humidityPct float64, err error)
}

// PCBTempSensor reads board temperature and the over-temp alert state.
type PCBTempSensor interface {
	Temperature() (float64, error)
	OverTemp() (bool, error)
}

// PowerReading is an INA219 sample.
type PowerReading struct {
	BusVoltage   float64
	ShuntVoltage float64
	Current      float64
	Power        float64
}

// PowerSensor reads pump power telemetry.
type PowerSensor interface {
	Read() (PowerReading, error)
}

// Camera captures a JPEG frame.
type Camera interface {
	Capture() ([]byte, error)
}

// ButtonEvent is a debounced button gesture.
type ButtonEvent int

const (
	SinglePress ButtonEvent = iota
	DoublePress
)

// Button delivers debounced press gestures.
type Button interface {
	Events() <-chan ButtonEvent
}

// Devices bundles the hardware the service controls. Sensor fields may be nil
// when a sensor failed to initialize (mirrors the Python "sensor == None" guard).
type Devices struct {
	Light       Light
	Pump        Pump
	Distance    DistanceSensor
	Env         EnvSensor
	PCBTemp     PCBTempSensor
	Power       PowerSensor
	UpperCamera Camera
	LowerCamera Camera
	Button      Button
}
