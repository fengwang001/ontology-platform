package catalog

import (
	"errors"
	"strings"
	"testing"

	"ontology/stats"
)

func withCols(rows int64, cols ...string) *stats.Table {
	t := &stats.Table{Name: "x", Rows: rows, Columns: map[string]stats.Column{}}
	for _, c := range cols {
		t.Columns[c] = stats.Column{NDV: 10}
	}
	return t
}

func TestRegistration(t *testing.T) {
	cases := []struct {
		name string
		run  func(*Catalog) error
		want error
	}{
		{"duplicate table", func(c *Catalog) error {
			_ = c.AddTable("a", 1, nil)
			return c.AddTable("a", 1, nil)
		}, ErrDuplicateTable},
		{"negative rows", func(c *Catalog) error {
			return c.AddTable("a", -1, nil)
		}, ErrCorruptStats},
		{"corrupt histogram rejected", func(c *Catalog) error {
			st := withCols(2, "c")
			col := st.Columns["c"]
			col.Histogram = &stats.Histogram{Bounds: []float64{0, 0, 1}, Buckets: []uint64{1, 1}}
			st.Columns["c"] = col
			return c.AddTable("a", 2, st)
		}, ErrCorruptStats},
		{"self join rejected", func(c *Catalog) error {
			_ = c.AddTable("a", 1, nil)
			return c.AddPredicate("a", "x", "a", "y")
		}, ErrSelfJoin},
		{"unknown table in predicate", func(c *Catalog) error {
			return c.AddPredicate("a", "x", "b", "y")
		}, ErrUnknownTable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.run(New())
			if !errors.Is(err, tc.want) {
				t.Fatalf("err=%v, want errors.Is %v", err, tc.want)
			}
		})
	}
}

func TestCorruptErrorNamesTableAndColumn(t *testing.T) {
	st := withCols(3, "c")
	col := st.Columns["c"]
	col.Histogram = stats.NewEquiWidth(0, 10, []uint64{1, 1}) // sums to 2 != 3
	st.Columns["c"] = col
	st.Name = "orders"
	err := New().AddTable("orders", 3, st)
	if err == nil || !errors.Is(err, ErrCorruptStats) {
		t.Fatalf("want corrupt error, got %v", err)
	}
	msg := err.Error()
	for _, want := range []string{"orders", "c"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("error %q missing %q", msg, want)
		}
	}
}

func TestCheckDiagnostics(t *testing.T) {
	c := New()
	stale := withCols(1000, "k") // catalog will declare 100 rows: drift 90%
	_ = c.AddTable("stale_tab", 100, stale)
	_ = c.AddTable("no_stats", 50, nil)
	_ = c.AddTable("fresh", 10, withCols(10, "k"))
	_ = c.AddPredicate("stale_tab", "k", "fresh", "k")
	_ = c.AddPredicate("no_stats", "k", "fresh", "k")

	var missing, staleCount int
	for _, err := range c.Check() {
		switch {
		case errors.Is(err, ErrMissingStats):
			missing++
		case errors.Is(err, ErrStaleStats):
			staleCount++
		default:
			t.Fatalf("unexpected diagnostic %v", err)
		}
	}
	if missing == 0 || staleCount != 1 {
		t.Fatalf("missing=%d stale=%d, want missing>0 stale=1", missing, staleCount)
	}
	info, _ := c.Table("stale_tab")
	if !info.Stale {
		t.Fatal("stale_tab not flagged stale")
	}
	fresh, _ := c.Table("fresh")
	if fresh.Stale {
		t.Fatal("fresh table wrongly flagged stale")
	}
}
