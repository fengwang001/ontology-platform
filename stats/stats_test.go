package stats

import (
	"errors"
	"math"
	"testing"
)

func mkTable(name string, rows int64, cols map[string]*Column) *TableStats {
	return &TableStats{Table: name, Rows: rows, Pages: rows, Cols: cols}
}

func TestSelectivity(t *testing.T) {
	unitHist := func(n int64) *Histogram {
		var bs []Bucket
		for k := int64(0); k < n; k++ {
			bs = append(bs, Bucket{Lo: float64(k), Hi: float64(k + 1), Count: 1})
		}
		return &Histogram{Buckets: bs}
	}
	cases := []struct {
		name     string
		left     *TableStats
		right    *TableStats
		lc, rc   string
		wantSel  float64
		wantMiss bool
	}{
		{"ndv", mkTable("l", 100, map[string]*Column{"a": {Name: "a", NDV: 10, HasStats: true}}),
			mkTable("r", 100, map[string]*Column{"b": {Name: "b", NDV: 20, HasStats: true}}),
			"a", "b", 0.05, false},
		{"missing left", mkTable("l", 100, map[string]*Column{}),
			mkTable("r", 100, map[string]*Column{"b": {Name: "b", NDV: 2, HasStats: true}}),
			"a", "b", DefaultSelectivity, true},
		{"missing both", mkTable("l", 100, nil), mkTable("r", 100, nil), "a", "b",
			DefaultSelectivity, true},
		{"hist unit", mkTable("l", 4, map[string]*Column{"a": {Name: "a", NDV: 4, Hist: unitHist(4), HasStats: true}}),
			mkTable("r", 4, map[string]*Column{"b": {Name: "b", NDV: 4, Hist: unitHist(4), HasStats: true}}),
			"a", "b", 0.25, false},
		{"zero ndv falls back", mkTable("l", 3, map[string]*Column{"a": {Name: "a", NDV: 0, HasStats: true}}),
			mkTable("r", 3, map[string]*Column{"b": {Name: "b", NDV: 0, HasStats: true}}),
			"a", "b", DefaultSelectivity, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			est := NewEstimator(map[string]*TableStats{"l": tc.left, "r": tc.right},
				func(string) int64 { return 0 })
			sel, notes, err := est.EquiSelectivity("l", tc.lc, "r", tc.rc)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if math.Abs(sel-tc.wantSel) > 1e-12 {
				t.Fatalf("sel = %v, want %v", sel, tc.wantSel)
			}
			gotMiss := false
			for _, n := range notes {
				if n.Missing {
					gotMiss = true
				}
			}
			if gotMiss != tc.wantMiss {
				t.Fatalf("missing flag = %v, want %v (notes=%v)", gotMiss, tc.wantMiss, notes)
			}
		})
	}
}

func TestHistogramValidate(t *testing.T) {
	ok := &Histogram{Buckets: []Bucket{{0, 1, 4}, {1, 2, 6}}}
	sumBad := &Histogram{Buckets: []Bucket{{0, 1, 4}, {1, 2, 5}}}
	orderBad := &Histogram{Buckets: []Bucket{{1, 2, 5}, {0, 1, 5}}}
	widthBad := &Histogram{Buckets: []Bucket{{1, 1, 5}}}
	cases := []struct {
		name string
		h    *Histogram
		rows int64
		want bool
	}{
		{"valid", ok, 10, false},
		{"sum mismatch", sumBad, 10, true},
		{"boundary not increasing", orderBad, 10, true},
		{"zero width", widthBad, 5, true},
		{"nil histogram", nil, 10, false},
		{"zero rows empty", &Histogram{}, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.h.Validate(tc.rows)
			if (err != nil) != tc.want || (err != nil && !errors.Is(err, ErrStatCorrupt)) {
				t.Fatalf("Validate err = %v, want corrupt=%v", err, tc.want)
			}
		})
	}
}

func TestEstimatorNeverReadsRows(t *testing.T) {
	tables := map[string]*TableStats{}
	// 逐张表登记：估计器全程只允许触碰统计对象。
	for i := 0; i < 8; i++ {
		name := string(rune('A' + i))
		tables[name] = mkTable(name, 100,
			map[string]*Column{"k": {Name: "k", NDV: int64(10 + i), HasStats: true}})
	}
	est := NewEstimator(tables, func(string) int64 { return 100 })
	for i := 0; i < 8; i++ {
		a := string(rune('A' + i))
		b := string(rune('A' + ((i + 1) % 8)))
		if _, _, err := est.EquiSelectivity(a, "k", b, "k"); err != nil {
			t.Fatalf("estimate %s-%s: %v", a, b, err)
		}
	}
	if est.RowDataReads() != 0 {
		t.Fatalf("row data reads = %d, want 0", est.RowDataReads())
	}
	if est.StatsLookups() == 0 {
		t.Fatal("stats lookups should be positive")
	}
}
