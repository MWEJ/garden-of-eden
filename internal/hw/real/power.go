package real

import (
	"github.com/iot-root/garden-of-eden/internal/hw"
)

func busVoltageV(raw uint16) float64             { return float64(raw>>3) * 0.004 }
func shuntVoltageV(raw int16) float64            { return float64(raw) * 0.00001 } // 10µV LSB
func currentA(shuntV, shuntOhms float64) float64 { return shuntV / shuntOhms }

// INA219 implements hw.PowerSensor.
type INA219 struct {
	bus       *Bus
	addr      uint16
	shuntOhms float64
}

func NewINA219(bus *Bus) *INA219 { return &INA219{bus: bus, addr: 0x40, shuntOhms: 0.08} }

func (s *INA219) readReg(reg byte) (uint16, error) {
	buf := make([]byte, 2)
	if err := s.bus.Tx(s.addr, []byte{reg}, buf); err != nil {
		return 0, err
	}
	return uint16(buf[0])<<8 | uint16(buf[1]), nil
}

func (s *INA219) Read() (hw.PowerReading, error) {
	busRaw, err := s.readReg(0x02) // bus voltage register
	if err != nil {
		return hw.PowerReading{}, err
	}
	shuntRaw, err := s.readReg(0x01) // shunt voltage register
	if err != nil {
		return hw.PowerReading{}, err
	}
	bv := busVoltageV(busRaw)
	sv := shuntVoltageV(int16(shuntRaw))
	cur := currentA(sv, s.shuntOhms)
	return hw.PowerReading{
		BusVoltage:   round2(bv),
		ShuntVoltage: sv,
		Current:      round2(cur),
		Power:        round2(bv * cur),
	}, nil
}

var _ hw.PowerSensor = (*INA219)(nil)
