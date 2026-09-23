package stats

import (
	"errors"
	"testing"
)

func TestValidate(t *testing.T) {
	cases := []struct {
		name    string
		stats   *TableStats
		wantErr bool
	}{
		{"valid", &TableStats{Table: "A", Rows: 100, Columns: map[string]*ColumnStats{
			"x": {Name: "x", NDV: 50, Hist: &Histogram{Bounds: []float64{0, 10, 20}, Counts: []uint64{40, 60}}},
		}}, false},
		{"no histogram", &TableStats{Table: "A", Rows: 5, Columns: map[string]*ColumnStats{
			"x": {Name: "x", NDV: 5},
		}}, false},
		{"bounds not increasing", &TableStats{Table: "A", Rows: 100, Columns: map[string]*ColumnStats{
			"x": {Name: "x", NDV: 50, Hist: &Histogram{Bounds: []float64{0, 0, 20}, Counts: []uint64{40, 60}}},
		}}, true},
		{"count sum mismatch", &TableStats{Table: "A", Rows: 100, Columns: map[string]*ColumnStats{
			"x": {Name: "x", NDV: 50, Hist: &Histogram{Bounds: []float64{0, 10, 20}, Counts: []uint64{40, 59}}},
		}}, true},
		{"bounds length mismatch", &TableStats{Table: "A", Rows: 100, Columns: map[string]*ColumnStats{
			"x": {Name: "x", NDV: 50, Hist: &Histogram{Bounds: []float64{0, 10}, Counts: []uint64{40, 60}}},
		}}, true},
	}
	for _, tc := range cases {
		err := tc.stats.Validate()
		if gotErr := errors.Is(err, ErrCorruptStats); gotErr != tc.wantErr {
			t.Errorf("%s: errors.Is(ErrCorruptStats)=%v, want %v (err=%v)", tc.name, gotErr, tc.wantErr, err)
		}
	}
}

func TestEqJoinSelectivity(t *testing.T) {
	cases := []struct {
		name       string
		ndvA, ndvB uint64
		want       float64
	}{
		{"max ndv", 100, 500, 1.0 / 500},
		{"symmetric", 7, 7, 1.0 / 7},
		{"zero ndv falls back", 0, 500, DefaultSelectivity},
		{"both zero falls back", 0, 0, DefaultSelectivity},
	}
	for _, tc := range cases {
		if got := EqJoinSelectivity(tc.ndvA, tc.ndvB); got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestRowDataReadsStartsAtZero(t *testing.T) {
	if got := RowDataReads(); got != 0 {
		t.Errorf("RowDataReads=%d, want 0", got)
	}
}
