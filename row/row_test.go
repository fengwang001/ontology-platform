package row

import "testing"

func TestCompare(t *testing.T) {
	cases := []struct {
		name string
		a, b Row
		want int
	}{
		{"score less", Row{1, "z"}, Row{2, "a"}, -1},
		{"score greater", Row{2, "a"}, Row{1, "z"}, 1},
		{"tie id less", Row{1, "a"}, Row{1, "b"}, -1},
		{"tie id greater", Row{1, "b"}, Row{1, "a"}, 1},
		{"identical", Row{1, "a"}, Row{1, "a"}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Compare(tc.a, tc.b); got != tc.want {
				t.Fatalf("Compare = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestKeyHelpers(t *testing.T) {
	cases := []struct {
		name                string
		r                   Row
		score               float64
		id                  string
		before, after       bool
	}{
		{"lower score", Row{0, "z"}, 1, "a", true, false},
		{"tie lower id", Row{1, "a"}, 1, "b", true, false},
		{"tie higher id", Row{1, "b"}, 1, "a", false, true},
		{"higher score", Row{2, "a"}, 1, "z", false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if Before(tc.r, tc.score, tc.id) != tc.before {
				t.Fatalf("Before = %v, want %v", !tc.before, tc.before)
			}
			if After(tc.r, tc.score, tc.id) != tc.after {
				t.Fatalf("After mismatch, want %v", tc.after)
			}
		})
	}
}

func TestIDs(t *testing.T) {
	got := IDs([]Row{{1, "a"}, {2, "b"}})
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("IDs = %v", got)
	}
}
