package real

import (
	"fmt"

	"github.com/iot-root/garden-of-eden/internal/hw"
	"github.com/warthog618/go-gpiocdev"
)

// decodePCT2075 converts the 2-byte temperature register to °C.
func decodePCT2075(hi, lo byte) float64 {
	raw := int16(uint16(hi)<<8|uint16(lo)) >> 5 // 11-bit, right-aligned
	return float64(raw) * 0.125
}

// PCT2075 implements hw.PCBTempSensor. alertLine is optional (may be nil).
type PCT2075 struct {
	bus       *Bus
	addr      uint16
	alertLine *gpiocdev.Line
}

func NewPCT2075(bus *Bus, chip string, alertGPIO int) (*PCT2075, error) {
	p := &PCT2075{bus: bus, addr: 0x48}

	// Configure to match the Python predecessor: OS pin active-HIGH (config
	// bit 2), comparator mode, Tos=36°C, Thyst=34°C. Tos/Thyst are
	// left-justified (high bits): 36°C -> 0x2400, 34°C -> 0x2200. Done before
	// requesting the GPIO so an absent sensor fails fast without leaking a line.
	if err := bus.Tx(p.addr, []byte{0x01, 0x04}, nil); err != nil { // Conf: OS_POL active-high
		return nil, fmt.Errorf("pct2075 config: %w", err)
	}
	if err := bus.Tx(p.addr, []byte{0x03, 0x24, 0x00}, nil); err != nil { // Tos = 36°C
		return nil, fmt.Errorf("pct2075 Tos: %w", err)
	}
	if err := bus.Tx(p.addr, []byte{0x02, 0x22, 0x00}, nil); err != nil { // Thyst = 34°C
		return nil, fmt.Errorf("pct2075 Thyst: %w", err)
	}

	if alertGPIO >= 0 {
		line, err := gpiocdev.RequestLine(chip, alertGPIO, gpiocdev.AsInput)
		if err != nil {
			return nil, err
		}
		p.alertLine = line
	}
	return p, nil
}

func (p *PCT2075) Temperature() (float64, error) {
	buf := make([]byte, 2)
	if err := p.bus.Tx(p.addr, []byte{0x00}, buf); err != nil { // 0x00 = temp register
		return 0, err
	}
	return decodePCT2075(buf[0], buf[1]), nil
}

func (p *PCT2075) OverTemp() (bool, error) {
	if p.alertLine == nil {
		return false, nil
	}
	v, err := p.alertLine.Value()
	if err != nil {
		return false, err
	}
	return v == 1, nil // OS polarity set active-high in NewPCT2075
}

// Close releases the alert GPIO line, if any.
func (p *PCT2075) Close() error {
	if p.alertLine != nil {
		return p.alertLine.Close()
	}
	return nil
}

var _ hw.PCBTempSensor = (*PCT2075)(nil)
