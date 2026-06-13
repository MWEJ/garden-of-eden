package real

import (
	"math"
	"testing"
)

func TestMedian(t *testing.T) {
	cases := []struct {
		in   []float64
		want float64
	}{
		{[]float64{3, 1, 2}, 2},
		{[]float64{4, 1, 3, 2}, 2.5},
		{[]float64{5}, 5},
	}
	for _, c := range cases {
		if got := median(c.in); math.Abs(got-c.want) > 1e-9 {
			t.Errorf("median(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestMedianOrErrEmpty(t *testing.T) {
	if _, err := medianOrErr(nil); err == nil {
		t.Error("expected error for empty input")
	}
}
