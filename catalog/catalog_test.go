package catalog

import (
	"errors"
	"testing"

	"ontology/stats"
)

func goodHist(rows int64) *stats.Histogram {
	return &stats.Histogram{Bounds: []float64{0, 1, 2}, Counts: []int64{rows / 2, rows - rows/2}}
}

func TestCorruptOnRegister(t *testing.T) {
	cases := []struct {
		name      string
		hist      *stats.Histogram
		statRows  int64
		wantError bool
	}{
		{"healthy", goodHist(100), 100, false},
		{"sum mismatch", &stats.Histogram{Bounds: []float64{0, 1, 2}, Counts: []int64{30, 30}}, 100, true},
		{"bad bounds", &stats.Histogram{Bounds: []float64{0, 2, 2}, Counts: []int64{50, 50}}, 100, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := New()
			err := c.Register(&Table{Name: "t", Rows: 100, Stat: &stats.Table{
				Name: "t", Rows: tc.statRows,
				Columns: map[string]*stats.Column{"x": {Name: "x", NDV: 4, Hist: tc.hist}},
			}})
			if tc.wantError && !errors.Is(err, ErrCorruptStats) {
				t.Fatalf("want ErrCorruptStats, got %v", err)
			}
			if !tc.wantError && err != nil {
				t.Fatalf("unexpected err %v", err)
			}
		})
	}
}

func TestStaleAndMissing(t *testing.T) {
	c := New()
	_ = c.Register(&Table{Name: "a", Rows: 100, Stat: &stats.Table{
		Name: "a", Rows: 200, Columns: map[string]*stats.Column{"x": {Name: "x", NDV: 5}}}})
	_ = c.Register(&Table{Name: "b", Rows: 10, Stat: &stats.Table{
		Name: "b", Rows: 10, Columns: map[string]*stats.Column{}}})

	tbl := c.TableByName("a")
	if ws := TableWarnings(tbl); !HasWarningKind(ws, ErrStaleStats) {
		t.Fatalf("want stale warning, got %v", ws)
	}
	tblFresh := c.TableByName("b")
	if ws := TableWarnings(tblFresh); len(ws) != 0 {
		t.Fatalf("want no warning, got %v", ws)
	}

	_, _, ws := c.Resolve(&Predicate{LeftTable: "a", LeftCol: "x", RightTable: "b", RightCol: "y"})
	if !HasWarningKind(ws, ErrMissingStats) {
		t.Fatalf("want missing warning, got %v", ws)
	}

	if err := c.AddPredicate(&Predicate{LeftTable: "a", LeftCol: "x", RightTable: "a", RightCol: "y"}); !errors.Is(err, ErrSelfJoin) {
		t.Fatalf("want ErrSelfJoin, got %v", err)
	}
}
