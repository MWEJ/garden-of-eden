package real

import (
	"math"
	"testing"
)

func TestPCT2075Decode(t *testing.T) {
	cases := []struct {
		hi, lo byte
		want   float64
	}{
		{0x19, 0x00, 25.0}, // 0x1900>>5 = 200; 200*0.125 = 25.0
		{0x00, 0x00, 0.0},
		{0xFF, 0x80, -0.5}, // negative (two's complement, 11-bit)
	}
	for _, c := range cases {
		if got := decodePCT2075(c.hi, c.lo); math.Abs(got-c.want) > 0.01 {
			t.Errorf("decode(%02x%02x) = %v, want %v", c.hi, c.lo, got, c.want)
		}
	}
}
