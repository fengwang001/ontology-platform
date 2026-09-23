package cost

import (
	"math"
	"testing"
)

func TestCostFormulas(t *testing.T) {
	cases := []struct {
		name string
		got  float64
		want float64
	}{
		{"scan", ScanCost(100), 100},
		{"join card", JoinCardinality(1000, 10, 0.2), 2000},
		{"join cost", JoinCost(1000, 10), 10000},
		{"tree", TreeCost([]float64{1, 2}, []float64{10}), 13},
		{"zero scan", ScanCost(0), 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if math.Abs(tc.got-tc.want) > 1e-9 {
				t.Fatalf("got %v, want %v", tc.got, tc.want)
			}
		})
	}
}

func TestEqual(t *testing.T) {
	ulp := math.Nextafter(1e16, math.Inf(1)) - 1e16
	cases := []struct {
		name string
		a    float64
		b    float64
		want bool
	}{
		{"identical", 100, 100, true},
		{"one ulp apart at 1e16", 1e16, 1e16 + ulp, true},
		{"within epsilon", 1e6, 1e6 + 1e-7, true},
		{"real difference", 100, 101, false},
		{"zero scale", 0, 1e-13, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Equal(tc.a, tc.b); got != tc.want {
				t.Fatalf("Equal(%v,%v)=%v want %v", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

func TestPrefer(t *testing.T) {
	ulp := math.Nextafter(1e16, math.Inf(1)) - 1e16
	cases := []struct {
		name      string
		cur       float64
		cand      float64
		curNames  []string
		candNames []string
		want      bool
	}{
		{"lower wins", 100, 99, []string{"a"}, []string{"b"}, true},
		{"higher loses", 99, 100, []string{"a"}, []string{"b"}, false},
		{"tie lexicographic smaller", 1e16, 1e16 + ulp, []string{"b"}, []string{"a"}, true},
		{"tie lexicographic larger", 1e16, 1e16 + ulp, []string{"a"}, []string{"b"}, false},
		{"tie identical names keeps", 10, 10, []string{"a"}, []string{"a"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Prefer(tc.cur, tc.cand, tc.curNames, tc.candNames); got != tc.want {
				t.Fatalf("Prefer=%v want %v", got, tc.want)
			}
		})
	}
}
