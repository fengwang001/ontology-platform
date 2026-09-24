package row

import (
	"math"
	"testing"
)

func TestCompare(t *testing.T) {
	cases := []struct {
		name string
		a, b Row
		want int
	}{
		{"score less", Row{1, "x"}, Row{2, "x"}, -1},
		{"score greater", Row{2, "x"}, Row{1, "x"}, 1},
		{"tie id less", Row{1, "a"}, Row{1, "b"}, -1},
		{"tie id greater", Row{1, "b"}, Row{1, "a"}, 1},
		{"equal key", Row{1, "a"}, Row{1, "a"}, 0},
		{"negative", Row{-2, "z"}, Row{-1, "a"}, -1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Compare(tc.a, tc.b); got != tc.want {
				t.Fatalf("Compare = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestValid(t *testing.T) {
	cases := []struct {
		name string
		r    Row
		want bool
	}{
		{"normal", Row{1.5, "id1"}, true},
		{"zero score", Row{0, "id1"}, true},
		{"empty id", Row{1, ""}, false},
		{"nan", Row{math.NaN(), "id1"}, false},
		{"inf", Row{math.Inf(1), "id1"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Valid(tc.r); got != tc.want {
				t.Fatalf("Valid = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestLess(t *testing.T) {
	cases := []struct {
		a, b Row
		want bool
	}{
		{Row{1, "a"}, Row{1, "b"}, true},
		{Row{1, "b"}, Row{1, "a"}, false},
		{Row{1, "a"}, Row{1, "a"}, false},
	}
	for _, tc := range cases {
		if got := Less(tc.a, tc.b); got != tc.want {
			t.Fatalf("Less(%v,%v) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}
