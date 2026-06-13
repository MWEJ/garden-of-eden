package real

import (
	"math"
	"testing"
)

func TestINA219BusVoltage(t *testing.T) {
	// 0x1F40 = 8000 raw; >>3 = 1000; ×0.004 V = 4.000 V
	if got := busVoltageV(0x1F40); math.Abs(got-4.0) > 1e-9 {
		t.Errorf("busVoltageV = %v, want 4.0", got)
	}
}

func TestINA219ShuntAndCurrent(t *testing.T) {
	// shunt raw 1000 × 10µV = 0.01 V; current = 0.01 / 0.08 = 0.125 A
	v := shuntVoltageV(1000)
	if math.Abs(v-0.01) > 1e-9 {
		t.Fatalf("shuntVoltageV = %v", v)
	}
	if got := currentA(v, 0.08); math.Abs(got-0.125) > 1e-9 {
		t.Errorf("currentA = %v, want 0.125", got)
	}
	if got := shuntVoltageV(-500); math.Abs(got-(-0.005)) > 1e-9 {
		t.Errorf("shuntVoltageV(-500) = %v, want -0.005", got)
	}
	if got := currentA(-0.005, 0.08); math.Abs(got-(-0.0625)) > 1e-9 {
		t.Errorf("currentA(neg) = %v, want -0.0625", got)
	}
}
