package real

import (
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
	return v == 1, nil // active-high alert
}

var _ hw.PCBTempSensor = (*PCT2075)(nil)
