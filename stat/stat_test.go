package stat

import (
	"math"
	"testing"
)

func TestSharesAndDeviation(t *testing.T) {
	cases := []struct {
		name    string
		served  map[string]float64
		weights map[string]float64
		wantDev float64
	}{
		{"exact 1:3:6",
			map[string]float64{"a": 10, "b": 30, "c": 60},
			map[string]float64{"a": 1, "b": 3, "c": 6}, 0},
		{"a 10% over",
			map[string]float64{"a": 11, "b": 30, "c": 60},
			map[string]float64{"a": 1, "b": 3, "c": 6}, 0.0892},
		{"equal weights",
			map[string]float64{"x": 50, "y": 50},
			map[string]float64{"x": 1, "y": 1}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tr := New()
			total := 0.0
			for id, c := range tc.served {
				tr.Record(id, c)
				total += c
			}
			for id, c := range tc.served {
				if got := tr.Share(id); math.Abs(got-c/total) > 1e-12 {
					t.Errorf("Share(%s)=%.6f want %.6f", id, got, c/total)
				}
			}
			if dev := tr.MaxDeviation(tc.weights); math.Abs(dev-tc.wantDev) > 1e-3 {
				t.Errorf("MaxDeviation=%.4f want %.4f", dev, tc.wantDev)
			}
		})
	}
	if got := New().Share("nobody"); got != 0 {
		t.Errorf("empty tracker Share=%v want 0", got)
	}
}
