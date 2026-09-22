package cost

import (
	"errors"
	"math"
	"testing"

	"ontology/catalog"
	"ontology/stats"
)

func buildCatalog() *catalog.Catalog {
	c := catalog.New()
	_ = c.Register(&catalog.Table{Name: "a", Rows: 100, Stat: &stats.Table{
		Name: "a", Rows: 100, Columns: map[string]*stats.Column{"id": {Name: "id", NDV: 100}}}})
	_ = c.Register(&catalog.Table{Name: "b", Rows: 50, Stat: &stats.Table{
		Name: "b", Rows: 50, Columns: map[string]*stats.Column{
			"aid": {Name: "aid", NDV: 100}, "cid": {Name: "cid", NDV: 25}}}})
	_ = c.Register(&catalog.Table{Name: "c", Rows: 200, Stat: &stats.Table{
		Name: "c", Rows: 200, Columns: map[string]*stats.Column{"id": {Name: "id", NDV: 25}}}})
	_ = c.AddPredicate(&catalog.Predicate{LeftTable: "a", LeftCol: "id", RightTable: "b", RightCol: "aid"})
	_ = c.AddPredicate(&catalog.Predicate{LeftTable: "b", LeftCol: "cid", RightTable: "c", RightCol: "id"})
	return c
}

func TestScanJoinCost(t *testing.T) {
	cases := []struct {
		name             string
		rows             int64
		costL, costR     float64
		cardL, cardR     float64
		wantScan, wantJo float64
	}{
		{"100 rows one page", 100, 0, 0, 0, 0, 110, 0},
		{"101 rows two pages", 101, 0, 0, 0, 0, 121, 0},
		{"join asymmetric", 0, 3, 4, 10, 100, 0, 3 + 4 + 20 + 100},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ScanCost(tc.rows); math.Abs(got-tc.wantScan) > 1e-9 {
				t.Fatalf("scan got %v want %v", got, tc.wantScan)
			}
			if got := JoinCost(tc.costL, tc.costR, tc.cardL, tc.cardR); math.Abs(got-tc.wantJo) > 1e-9 {
				t.Fatalf("join got %v want %v", got, tc.wantJo)
			}
		})
	}
}

func TestCrossAndCounters(t *testing.T) {
	c := buildCatalog()
	e := NewEstimator(c)

	cases := []struct {
		name      string
		l, r      []string
		wantSels  []float64
		wantPreds int
		checkWarn bool
	}{
		{"a-b edge", []string{"a"}, []string{"b"}, []float64{0.01}, 1, false},
		{"b-c edge", []string{"b"}, []string{"c"}, []float64{0.04}, 1, false},
		{"ab-c edge only b-c", []string{"a", "b"}, []string{"c"}, []float64{0.04}, 1, false},
		{"a-c cartesian", []string{"a"}, []string{"c"}, nil, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sels, _, n := e.Cross(tc.l, tc.r)
			if n != tc.wantPreds || len(sels) != len(tc.wantSels) {
				t.Fatalf("got sels=%v n=%d, want n=%d", sels, n, tc.wantPreds)
			}
			for i := range sels {
				if math.Abs(sels[i]-tc.wantSels[i]) > 1e-12 {
					t.Fatalf("sel[%d] got %v want %v", i, sels[i], tc.wantSels[i])
				}
			}
		})
	}

	if e.Counters.RowDataAccesses != 0 {
		t.Fatalf("row data accesses = %d, want 0", e.Counters.RowDataAccesses)
	}

	c2 := catalog.New()
	_ = c2.Register(&catalog.Table{Name: "a", Rows: 10})
	_ = c2.Register(&catalog.Table{Name: "b", Rows: 10})
	_ = c2.AddPredicate(&catalog.Predicate{LeftTable: "a", LeftCol: "x", RightTable: "b", RightCol: "y"})
	e2 := NewEstimator(c2)
	sels, warns, n := e2.Cross([]string{"a"}, []string{"b"})
	if n != 1 || sels[0] != stats.DefaultSelectivity {
		t.Fatalf("got sels=%v n=%d", sels, n)
	}
	var found bool
	for _, w := range warns {
		if errors.Is(w.Kind, catalog.ErrMissingStats) {
			found = true
		}
	}
	if !found {
		t.Fatalf("want missing warning, got %v", warns)
	}
}
