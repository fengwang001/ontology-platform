package stats

import (
	"errors"
	"math"
	"testing"
)

func goodHist(rows int64) *Histogram {
	bounds := []float64{10, 20, 30}
	counts := []int64{rows / 2, rows - rows/2 - rows/2, rows / 2}
	counts[1] = rows - counts[0] - counts[2]
	return &Histogram{Width: 10, UpperBounds: bounds, Counts: counts}
}

func TestEquiSelectivity(t *testing.T) {
	cases := []struct {
		name     string
		left     *Column
		right    *Column
		wantSel  float64
		reliable bool
	}{
		{"equal ndv", &Column{NDV: 10}, &Column{NDV: 10}, 0.1, true},
		{"max ndv", &Column{NDV: 100}, &Column{NDV: 4}, 0.01, true},
		{"missing left", nil, &Column{NDV: 10}, DefaultSelectivity, false},
		{"missing right", &Column{NDV: 10}, nil, DefaultSelectivity, false},
		{"zero ndv", &Column{NDV: 0}, &Column{NDV: 5}, DefaultSelectivity, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, reliable := EquiSelectivity(tc.left, tc.right)
			if math.Abs(got-tc.wantSel) > 1e-15 || reliable != tc.reliable {
				t.Fatalf("got (%v,%v), want (%v,%v)", got, reliable, tc.wantSel, tc.reliable)
			}
		})
	}
}

func TestCombinePredicates(t *testing.T) {
	cases := []struct {
		name        string
		sels        []float64
		want        float64
		independent bool
	}{
		{"none", nil, 1, true},
		{"single", []float64{0.25}, 0.25, true},
		{"two independent assumption", []float64{0.5, 0.2}, 0.1, false},
		{"three", []float64{0.1, 0.1, 0.1}, 0.001, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, independent := CombinePredicates(tc.sels)
			if math.Abs(got-tc.want) > 1e-15 || independent != tc.independent {
				t.Fatalf("got (%v,%v), want (%v,%v)", got, independent, tc.want, tc.independent)
			}
		})
	}
}

func TestValidate(t *testing.T) {
	corruptSum := goodHist(100)
	corruptSum.Counts[0]++
	corruptBounds := goodHist(100)
	corruptBounds.UpperBounds[1] = 5
	negative := goodHist(100)
	negative.Counts[0] -= 2
	length := &Histogram{UpperBounds: []float64{1}, Counts: []int64{1, 2}}
	cases := []struct {
		name    string
		hist    *Histogram
		wantErr error
	}{
		{"valid", goodHist(100), nil},
		{"sum mismatch", corruptSum, ErrCorruptStats},
		{"bounds not increasing", corruptBounds, ErrCorruptStats},
		{"negative count", negative, ErrCorruptStats},
		{"length mismatch", length, ErrCorruptStats},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tab := &Table{Name: "t", Rows: 100, Columns: map[string]*Column{"c": {Name: "c", Hist: tc.hist}}}
			err := tab.Validate()
			if tc.wantErr == nil {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("got %v, want errors.Is %v", err, tc.wantErr)
			}
		})
	}
}

func TestColumnStatMissing(t *testing.T) {
	tab := &Table{Name: "t", Columns: map[string]*Column{"a": {Name: "a"}}}
	if _, err := tab.ColumnStat("missing"); !errors.Is(err, ErrMissingStats) {
		t.Fatalf("got %v, want ErrMissingStats", err)
	}
	if _, err := tab.ColumnStat("a"); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
}
