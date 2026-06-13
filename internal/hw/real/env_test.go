package real

import "testing"

func TestAM2320CRC(t *testing.T) {
	// Frame: function 0x03, length 0x04, 4 data bytes. CRC computed over the
	// first 6 bytes (little-endian result appended).
	frame := []byte{0x03, 0x04, 0x01, 0x90, 0x01, 0xF4}
	lo, hi := am2320CRC(frame)
	got := crc16(frame)
	if lo != byte(got) || hi != byte(got>>8) {
		t.Errorf("crc mismatch: split=%02x%02x full=%04x", hi, lo, got)
	}
	// Round-trip: appending the CRC then validating must pass.
	full := append(append([]byte{}, frame...), lo, hi)
	if !am2320Valid(full) {
		t.Error("am2320Valid rejected a self-consistent frame")
	}
	full[2] ^= 0xFF // corrupt a data byte
	if am2320Valid(full) {
		t.Error("am2320Valid accepted a corrupted frame")
	}
}

func TestAM2320KnownAnswerCRC(t *testing.T) {
	// Modbus CRC-16 of {0x03,0x04,0x01,0x90,0x01,0xF4} is 0x2EF0.
	lo, hi := am2320CRC([]byte{0x03, 0x04, 0x01, 0x90, 0x01, 0xF4})
	if lo != 0xF0 || hi != 0x2E {
		t.Errorf("known-answer CRC: got lo=%02x hi=%02x, want f0 2e", lo, hi)
	}
}

func TestDecodeAM2320Temp(t *testing.T) {
	cases := []struct {
		raw  uint16
		want float64
	}{
		{0x00FA, 25.0}, // +250/10
		{0x0000, 0.0},
		{0x8064, -10.0}, // sign bit + 100/10
		{0x8001, -0.1},
	}
	for _, c := range cases {
		if got := decodeAM2320Temp(c.raw); got != c.want {
			t.Errorf("decodeAM2320Temp(%#04x) = %v, want %v", c.raw, got, c.want)
		}
	}
}

func TestCRC8AHT20(t *testing.T) {
	// Sensirion CRC-8 known-answer: crc8(0xBE,0xEF) == 0x92.
	if got := crc8AHT20([]byte{0xBE, 0xEF}); got != 0x92 {
		t.Errorf("crc8AHT20(BEEF) = %02x, want 92", got)
	}
}
