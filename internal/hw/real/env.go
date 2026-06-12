package real

import (
	"fmt"
	"time"

	"github.com/iot-root/garden-of-eden/internal/hw"
)

// crc16 is the Modbus CRC-16 used by the AM2320.
func crc16(data []byte) uint16 {
	crc := uint16(0xFFFF)
	for _, b := range data {
		crc ^= uint16(b)
		for i := 0; i < 8; i++ {
			if crc&1 == 1 {
				crc = (crc >> 1) ^ 0xA001
			} else {
				crc >>= 1
			}
		}
	}
	return crc
}

// am2320CRC returns the low and high CRC bytes for the given frame.
func am2320CRC(frame []byte) (lo, hi byte) {
	c := crc16(frame)
	return byte(c & 0xFF), byte(c >> 8)
}

// decodeAM2320Temp converts the AM2320 16-bit temperature word (sign-magnitude:
// bit 15 = sign, lower 15 bits = tenths of a degree) to °C.
func decodeAM2320Temp(raw uint16) float64 {
	if raw&0x8000 != 0 {
		return -float64(raw&0x7FFF) / 10.0
	}
	return float64(raw) / 10.0
}

// crc8AHT20 computes the AHT20/Sensirion CRC-8 (poly 0x31, init 0xFF).
func crc8AHT20(data []byte) byte {
	crc := byte(0xFF)
	for _, b := range data {
		crc ^= b
		for i := 0; i < 8; i++ {
			if crc&0x80 != 0 {
				crc = (crc << 1) ^ 0x31
			} else {
				crc <<= 1
			}
		}
	}
	return crc
}

// am2320Valid checks a full frame (data + trailing little-endian CRC).
func am2320Valid(full []byte) bool {
	if len(full) < 4 {
		return false
	}
	body := full[:len(full)-2]
	lo, hi := full[len(full)-2], full[len(full)-1]
	wantLo, wantHi := am2320CRC(body)
	return lo == wantLo && hi == wantHi
}

// EnvAM2320 implements hw.EnvSensor for the AM2320 at 0x5C.
type EnvAM2320 struct {
	bus  *Bus
	addr uint16
}

func NewEnvAM2320(bus *Bus) *EnvAM2320 { return &EnvAM2320{bus: bus, addr: 0x5C} }

func (e *EnvAM2320) Read() (float64, float64, error) {
	// Wake (AM2320 sleeps; first tx wakes it and is NACKed).
	_ = e.bus.Tx(e.addr, []byte{0x00}, nil)
	time.Sleep(2 * time.Millisecond)
	// Read 4 registers starting at 0x00 (humidity hi/lo, temp hi/lo).
	if err := e.bus.Tx(e.addr, []byte{0x03, 0x00, 0x04}, nil); err != nil {
		return 0, 0, err
	}
	time.Sleep(2 * time.Millisecond)
	buf := make([]byte, 8) // fn, len, 4 data, crc lo, crc hi
	if err := e.bus.Tx(e.addr, nil, buf); err != nil {
		return 0, 0, err
	}
	if !am2320Valid(buf) {
		return 0, 0, fmt.Errorf("am2320 CRC mismatch")
	}
	hum := float64(uint16(buf[2])<<8|uint16(buf[3])) / 10.0
	temp := decodeAM2320Temp(uint16(buf[4])<<8 | uint16(buf[5]))
	return temp, hum, nil
}

// EnvAHT20 implements hw.EnvSensor for the AHT20/DHT20 at 0x38.
type EnvAHT20 struct {
	bus  *Bus
	addr uint16
}

func NewEnvAHT20(bus *Bus) *EnvAHT20 { return &EnvAHT20{bus: bus, addr: 0x38} }

func (e *EnvAHT20) Read() (float64, float64, error) {
	if err := e.bus.Tx(e.addr, []byte{0xAC, 0x33, 0x00}, nil); err != nil {
		return 0, 0, err
	}
	time.Sleep(80 * time.Millisecond)
	buf := make([]byte, 7)
	if err := e.bus.Tx(e.addr, nil, buf); err != nil {
		return 0, 0, err
	}
	if buf[0]&0x80 != 0 {
		return 0, 0, fmt.Errorf("aht20 still busy")
	}
	if crc8AHT20(buf[:6]) != buf[6] {
		return 0, 0, fmt.Errorf("aht20 CRC mismatch")
	}
	rawHum := (uint32(buf[1]) << 12) | (uint32(buf[2]) << 4) | (uint32(buf[3]) >> 4)
	rawTemp := ((uint32(buf[3]) & 0x0F) << 16) | (uint32(buf[4]) << 8) | uint32(buf[5])
	hum := float64(rawHum) * 100.0 / 1048576.0
	temp := float64(rawTemp)*200.0/1048576.0 - 50.0
	return temp, hum, nil
}

var _ hw.EnvSensor = (*EnvAM2320)(nil)
var _ hw.EnvSensor = (*EnvAHT20)(nil)
