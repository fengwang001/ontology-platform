package stats

import (
	"errors"
	"testing"
)

func TestEqSelectivity(t *testing.T) {
	mk := func(ndv float64) *Table {
		return &Table{Name: "t", Rows: 100, Columns: map[string]Column{"c": {NDV: ndv}}}
	}
	cases := []struct {
		name         string
		left, right  *Table
		lcol, rcol   string
		wantSel      float64
		wantReliable bool
	}{
		{"both present, max NDV wins", mk(10), mk(50), "c", "c", 0.02, true},
		{"missing left column", mk(10), mk(50), "nope", "c", DefaultSelectivity, false},
		{"missing right column", mk(10), mk(50), "c", "nope", DefaultSelectivity, false},
		{"nil table", nil, mk(50), "c", "c", DefaultSelectivity, false},
		{"zero NDV clamped to 1", mk(0), mk(0), "c", "c", 1.0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := EqSelectivity(tc.left, tc.lcol, tc.right, tc.rcol)
			if got.Sel != tc.wantSel || got.Reliable != tc.wantReliable {
				t.Fatalf("got %+v, want sel=%v reliable=%v", got, tc.wantSel, tc.wantReliable)
			}
		})
	}
}

func TestValidate(t *testing.T) {
	good := NewEquiWidth(0, 100, []uint64{40, 60})
	cases := []struct {
		name    string
		table   *Table
		wantErr bool
	}{
		{"valid histogram", &Table{Name: "a", Rows: 100, Columns: map[string]Column{"c": {NDV: 2, Histogram: good}}}, false},
		{"bucket sum mismatch", &Table{Name: "a", Rows: 100, Columns: map[string]Column{"c": {Histogram: NewEquiWidth(0, 100, []uint64{40, 61})}}}, true},
		{"non-increasing bounds", &Table{Name: "a", Rows: 2, Columns: map[string]Column{"c": {Histogram: &Histogram{Bounds: []float64{0, 0, 1}, Buckets: []uint64{1, 1}}}}}, true},
		{"wrong bounds length", &Table{Name: "a", Rows: 2, Columns: map[string]Column{"c": {Histogram: &Histogram{Bounds: []float64{0, 1}, Buckets: []uint64{1, 1}}}}}, true},
		{"negative rows", &Table{Name: "a", Rows: -1}, true},
		{"negative NDV", &Table{Name: "a", Rows: 1, Columns: map[string]Column{"c": {NDV: -2}}}, true},
		{"no columns is fine", &Table{Name: "a", Rows: 5}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.table.Validate()
			if (err != nil) != tc.wantErr {
				t.Fatalf("err=%v, wantErr=%v", err, tc.wantErr)
			}
			if tc.wantErr && !errors.Is(err, ErrCorrupt) {
				t.Fatalf("err %v does not wrap ErrCorrupt", err)
			}
		})
	}
}

func TestNoRowAccess(t *testing.T) {
	tab := &Table{Name: "t", Rows: 10, Columns: map[string]Column{"c": {NDV: 4}}}
	_ = EqSelectivity(tab, "c", tab, "c")
	if got := RowAccesses(); got != 0 {
		t.Fatalf("estimation touched row data %d times", got)
	}
}
