package cost

import "testing"

func TestCostModel(t *testing.T) {
	cases := []struct {
		name string
		got  float64
		want float64
	}{
		{"scan cost equals rows", ScanCost(1234), 1234},
		{"join cost is build+probe", JoinCost(100, 50), 150},
		{"total cost accumulates", TotalCost(100, 50, 10, 20), 180},
		{"join card with one predicate", JoinCard(1000, 2000, 0.001), 2000},
		{"join card predicates multiply", JoinCard(1000, 2000, 0.1, 0.01), 2000},
		{"join card no predicates is cross product", JoinCard(1000, 2000), 2_000_000},
		{"zero rows scan", ScanCost(0), 0},
		{"zero card join", JoinCard(0, 2000, 0.5), 0},
	}
	for _, tc := range cases {
		if tc.got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, tc.got, tc.want)
		}
	}
}
