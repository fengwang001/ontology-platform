package catalog

import (
	"errors"
	"testing"

	"ontology/stats"
)

func tableWithHist(table, col string, rows uint64, counts ...uint64) *stats.TableStats {
	bounds := make([]float64, len(counts)+1)
	for i := range bounds {
		bounds[i] = float64(i)
	}
	return &stats.TableStats{Table: table, Rows: rows, Columns: map[string]*stats.ColumnStats{
		col: {Name: col, NDV: 10, Hist: &stats.Histogram{Bounds: bounds, Counts: counts}},
	}}
}

func TestAnalyzeAnomalies(t *testing.T) {
	mk := func() *Catalog {
		c := New()
		c.AddTable("A", 1000)
		c.AddTable("B", 2000)
		if err := c.AddPredicate(ColRef{"A", "x"}, ColRef{"B", "y"}); err != nil {
			t.Fatal(err)
		}
		return c
	}
	cases := []struct {
		name      string
		mutate    func(c *Catalog)
		wantWarn  error // 期望的警告哨兵，nil 表示无警告
		wantErr   error // 期望的硬错误哨兵
		checkView func(t *testing.T, v *View)
	}{
		{"healthy", func(c *Catalog) {
			c.SetStats(&stats.TableStats{Table: "A", Rows: 1000, Columns: map[string]*stats.ColumnStats{"x": {Name: "x", NDV: 500}}})
			c.SetStats(&stats.TableStats{Table: "B", Rows: 2000, Columns: map[string]*stats.ColumnStats{"y": {Name: "y", NDV: 800}}})
		}, nil, nil, func(t *testing.T, v *View) {
			if v.Preds[0].Sel != 1.0/800 || v.Preds[0].Unreliable {
				t.Errorf("unexpected pred: %+v", v.Preds[0])
			}
		}},
		{"missing column stats falls back", func(c *Catalog) {
			c.SetStats(&stats.TableStats{Table: "A", Rows: 1000, Columns: map[string]*stats.ColumnStats{"x": {Name: "x", NDV: 500}}})
		}, ErrMissingStats, nil, func(t *testing.T, v *View) {
			if v.Preds[0].Sel != stats.DefaultSelectivity || !v.Preds[0].Unreliable {
				t.Errorf("unexpected pred: %+v", v.Preds[0])
			}
		}},
		{"stale stats corrected to catalog rows", func(c *Catalog) {
			c.SetStats(&stats.TableStats{Table: "A", Rows: 500, Columns: map[string]*stats.ColumnStats{"x": {Name: "x", NDV: 500}}})
			c.SetStats(&stats.TableStats{Table: "B", Rows: 2000, Columns: map[string]*stats.ColumnStats{"y": {Name: "y", NDV: 800}}})
		}, ErrStaleStats, nil, func(t *testing.T, v *View) {
			if !v.Tables[0].Stale || v.Tables[0].Rows != 1000 {
				t.Errorf("unexpected table: %+v", v.Tables[0])
			}
		}},
		{"fresh stats not flagged stale", func(c *Catalog) {
			c.SetStats(&stats.TableStats{Table: "A", Rows: 1100, Columns: map[string]*stats.ColumnStats{"x": {Name: "x", NDV: 500}}})
			c.SetStats(&stats.TableStats{Table: "B", Rows: 2000, Columns: map[string]*stats.ColumnStats{"y": {Name: "y", NDV: 800}}})
		}, nil, nil, func(t *testing.T, v *View) {
			if v.Tables[0].Stale {
				t.Errorf("fresh stats flagged stale: %+v", v.Tables[0])
			}
		}},
		{"corrupt histogram is hard error", func(c *Catalog) {
			c.SetStats(tableWithHist("A", "x", 1000, 400, 500))
		}, nil, stats.ErrCorruptStats, nil},
	}
	for _, tc := range cases {
		c := mk()
		tc.mutate(c)
		v, warnings, err := c.Analyze()
		if !errors.Is(err, tc.wantErr) {
			t.Errorf("%s: err=%v, want errors.Is %v", tc.name, err, tc.wantErr)
		}
		if tc.wantErr != nil {
			continue
		}
		found := false
		for _, w := range warnings {
			if errors.Is(w, tc.wantWarn) {
				found = true
			}
		}
		if tc.wantWarn != nil && !found {
			t.Errorf("%s: no warning matching %v in %v", tc.name, tc.wantWarn, warnings)
		}
		if tc.wantWarn == nil && len(warnings) != 0 {
			t.Errorf("%s: unexpected warnings %v", tc.name, warnings)
		}
		if tc.checkView != nil {
			tc.checkView(t, v)
		}
	}
}

func TestAddPredicateRejects(t *testing.T) {
	c := New()
	c.AddTable("A", 10)
	cases := []struct {
		name  string
		left  ColRef
		right ColRef
		want  error
	}{
		{"self join rejected", ColRef{"A", "x"}, ColRef{"A", "y"}, ErrSelfJoin},
		{"unknown table rejected", ColRef{"A", "x"}, ColRef{"ZZZ", "y"}, ErrUnknownTable},
	}
	for _, tc := range cases {
		if err := c.AddPredicate(tc.left, tc.right); !errors.Is(err, tc.want) {
			t.Errorf("%s: err=%v, want errors.Is %v", tc.name, err, tc.want)
		}
	}
}
