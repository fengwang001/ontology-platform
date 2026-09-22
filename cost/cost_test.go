package cost

import "testing"

func TestModel(t *testing.T) {
	cases := []struct {
		name               string
		rows               int64
		lCard, rCard       float64
		lCost, rCost       float64
		sel                float64
		wantScan, wantJoin float64
		wantCard           float64
	}{
		{"unit", 1, 1, 1, 0, 0, 1, 1, 1, 1},
		{"scan equals rows", 1000, 10, 20, 5, 7, 0.5, 1000, 5 + 7 + 200.0, 100},
		{"cartesian sel=1", 0, 4, 6, 1, 2, 1, 0, 1 + 2 + 24.0, 24},
		{"zero rows", 0, 0, 0, 0, 0, 0.1, 0, 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Scan(tc.rows); got != tc.wantScan {
				t.Fatalf("Scan=%v want %v", got, tc.wantScan)
			}
			if got := Join(tc.lCard, tc.rCard, tc.lCost, tc.rCost); got != tc.wantJoin {
				t.Fatalf("Join=%v want %v", got, tc.wantJoin)
			}
			if got := Card(tc.lCard, tc.rCard, tc.sel); got != tc.wantCard {
				t.Fatalf("Card=%v want %v", got, tc.wantCard)
			}
		})
	}
}
