package explain

import (
	"errors"
	"strings"
	"testing"

	"ontology/catalog"
	"ontology/plan"
	"ontology/stats"
)

type tc struct {
	name     string
	build    func(t *testing.T) (*catalog.Catalog, []string)
	wantSubs []string // every entry must appear in the render
}

func TestRenderAnnotations(t *testing.T) {
	mkStats := func(rows int64, cols map[string]float64) *stats.Table {
		st := &stats.Table{Name: "x", Rows: rows, Columns: map[string]stats.Column{}}
		for c, n := range cols {
			st.Columns[c] = stats.Column{NDV: n}
		}
		return st
	}
	cases := []tc{
		{"simple join has card and cost",
			func(t *testing.T) (*catalog.Catalog, []string) {
				c := catalog.New()
				_ = c.AddTable("A", 1000, mkStats(1000, map[string]float64{"k": 100}))
				_ = c.AddTable("B", 2000, mkStats(2000, map[string]float64{"k": 100}))
				if err := c.AddPredicate("A", "k", "B", "k"); err != nil {
					t.Fatal(err)
				}
				return c, []string{"A", "B"}
			},
			[]string{"Join(A.k=B.k)", "card=", "cost=", "Scan A", "Scan B"}},
		{"missing stats flagged unreliable",
			func(t *testing.T) (*catalog.Catalog, []string) {
				c := catalog.New()
				_ = c.AddTable("A", 100, nil)
				_ = c.AddTable("B", 100, mkStats(100, map[string]float64{"k": 10}))
				if err := c.AddPredicate("A", "k", "B", "k"); err != nil {
					t.Fatal(err)
				}
				return c, []string{"A", "B"}
			},
			[]string{"[unreliable estimate]"}},
		{"stale stats show corrected catalog rows",
			func(t *testing.T) (*catalog.Catalog, []string) {
				c := catalog.New()
				_ = c.AddTable("A", 100, mkStats(1000, map[string]float64{"k": 10}))
				_ = c.AddTable("B", 100, mkStats(100, map[string]float64{"k": 10}))
				if err := c.AddPredicate("A", "k", "B", "k"); err != nil {
					t.Fatal(err)
				}
				return c, []string{"A", "B"}
			},
			[]string{"[stale stats: catalog rows 100 used]", "[stale stats]"}},
		{"multiple predicates note independence",
			func(t *testing.T) (*catalog.Catalog, []string) {
				c := catalog.New()
				_ = c.AddTable("A", 100, mkStats(100, map[string]float64{"x": 10, "y": 10}))
				_ = c.AddTable("B", 100, mkStats(100, map[string]float64{"x": 10, "y": 10}))
				if err := c.AddPredicate("A", "x", "B", "x"); err != nil {
					t.Fatal(err)
				}
				if err := c.AddPredicate("A", "y", "B", "y"); err != nil {
					t.Fatal(err)
				}
				return c, []string{"A", "B"}
			},
			[]string{"[independence assumed]"}},
		{"cartesian annotated",
			func(t *testing.T) (*catalog.Catalog, []string) {
				c := catalog.New()
				_ = c.AddTable("A", 10, nil)
				_ = c.AddTable("B", 10, nil)
				return c, []string{"A", "B"}
			},
			[]string{"CartesianJoin", "[cartesian]"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cat, names := tc.build(t)
			root, _, err := plan.Optimize(cat, names)
			if err != nil {
				t.Fatal(err)
			}
			out := Render(root, cat)
			for _, want := range tc.wantSubs {
				if !strings.Contains(out, want) {
					t.Fatalf("render %q missing %q", out, want)
				}
			}
			if !strings.HasSuffix(out, "\n") {
				t.Fatal("render does not end with newline")
			}
		})
	}
}

func TestCorruptStillRejectedBeforeRender(t *testing.T) {
	c := catalog.New()
	st := &stats.Table{Name: "A", Rows: 2, Columns: map[string]stats.Column{
		"k": {Histogram: &stats.Histogram{Bounds: []float64{0, 0, 1}, Buckets: []uint64{1, 1}}}}}
	err := c.AddTable("A", 2, st)
	if !errors.Is(err, catalog.ErrCorruptStats) {
		t.Fatalf("got %v", err)
	}
}
