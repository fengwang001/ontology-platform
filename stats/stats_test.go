package stats

import (
	"errors"
	"testing"
)

func goodHist(col string, rows int64) *ColumnStat {
	return &ColumnStat{
		Table: "t", Column: col, NDV: 10, RowCount: rows,
		Hist: &Histogram{Buckets: []Bucket{
			{Lo: 0, Hi: 10, Count: rows / 2},
			{Lo: 10, Hi: 20, Count: rows - rows/2},
		}},
	}
}

func TestEqSelectivityAndAnomalies(t *testing.T) {
	left := goodHist("a", 100)
	right := goodHist("b", 100)
	right.NDV = 20
	cases := []struct {
		name      string
		l, r      *ColumnStat
		wantSel   float64
		wantIs    error
		checkCorr bool
	}{
		{"ndv 取较大值", left, right, 1.0 / 20, nil, false},
		{"缺统计回退默认值", nil, right, DefaultEqSelectivity, ErrStatsMissing, false},
		{"两侧都缺", nil, nil, DefaultEqSelectivity, ErrStatsMissing, false},
		{"桶计数和不符损坏", corruptSum(), right, 0, ErrStatsCorrupt, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sel, err := EqSelectivity(tc.l, tc.r)
			if tc.wantIs != nil && !errors.Is(err, tc.wantIs) {
				t.Fatalf("err = %v, want errors.Is %v", err, tc.wantIs)
			}
			if tc.wantIs == nil && err != nil {
				t.Fatalf("unexpected err %v", err)
			}
			if tc.checkCorr {
				return
			}
			if sel != tc.wantSel {
				t.Fatalf("sel = %v, want %v", sel, tc.wantSel)
			}
		})
	}
}

func corruptSum() *ColumnStat {
	c := goodHist("a", 100)
	c.RowCount = 999
	return c
}

func TestValidateAndStale(t *testing.T) {
	badBounds := &ColumnStat{Table: "t", Column: "x", NDV: 1, RowCount: 10,
		Hist: &Histogram{Buckets: []Bucket{{Lo: 0, Hi: 5, Count: 5}, {Lo: 4, Hi: 8, Count: 5}}}}
	badSum := goodHist("y", 50)
	badSum.RowCount = 100
	negative := &ColumnStat{Table: "t", Column: "z", NDV: -1, RowCount: 1}
	cases := []struct {
		name   string
		stat   *ColumnStat
		wantIs error
	}{
		{"桶边界非递增", badBounds, ErrStatsCorrupt},
		{"桶计数之和不符", badSum, ErrStatsCorrupt},
		{"NDV 为负", negative, ErrStatsCorrupt},
		{"正常", goodHist("ok", 10), nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.stat.Validate()
			if tc.wantIs == nil && err != nil {
				t.Fatalf("unexpected %v", err)
			}
			if tc.wantIs != nil && !errors.Is(err, tc.wantIs) {
				t.Fatalf("err = %v, want %v", err, tc.wantIs)
			}
		})
	}
	for _, c := range []struct {
		stat, cat int64
		stale     bool
	}{{100, 100, false}, {110, 100, false}, {111, 100, true}, {0, 10, true}} {
		got, _ := Stale(c.stat, c.cat)
		if got != c.stale {
			t.Fatalf("Stale(%d,%d)=%v want %v", c.stat, c.cat, got, c.stale)
		}
	}
}

func TestRangeFraction(t *testing.T) {
	c := goodHist("a", 100)
	f, ok := c.RangeFraction(0, 10)
	if !ok || f != 0.5 {
		t.Fatalf("fraction = %v,%v want 0.5,true", f, ok)
	}
	f, ok = c.RangeFraction(0, 20)
	if !ok || f != 1.0 {
		t.Fatalf("fraction = %v,%v want 1,true", f, ok)
	}
}
