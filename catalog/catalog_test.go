package catalog

import (
	"errors"
	"sync"
	"testing"

	"ontology/stats"
)

func makeTable(name string, rows int64, cols ...string) *stats.Table {
	m := map[string]*stats.Column{}
	for _, c := range cols {
		m[c] = &stats.Column{Name: c, NDV: rows}
	}
	return &stats.Table{Name: name, Rows: rows, Columns: m}
}

func makeHistTable(name string, rows int64, corruptSum, corruptBounds bool) *stats.Table {
	counts := []int64{rows / 2, rows - rows/2}
	bounds := []float64{5, 10}
	if corruptSum {
		counts[0]++
	}
	if corruptBounds {
		bounds[1] = 4
	}
	return &stats.Table{Name: name, Rows: rows, Columns: map[string]*stats.Column{
		"x": {Name: "x", NDV: 10, Hist: &stats.Histogram{
			Width: 5, UpperBounds: bounds, Counts: counts}}}}
}

func TestValidate(t *testing.T) {
	cases := []struct {
		name      string
		build     func() *Catalog
		wantIssue error
		wantFatal error
	}{
		{"healthy", func() *Catalog {
			c := New()
			_ = c.AddTable("t", 100, makeTable("t", 100, "x"))
			return c
		}, nil, nil},
		{"stale", func() *Catalog {
			c := New()
			_ = c.AddTable("t", 200, makeTable("t", 100, "x"))
			return c
		}, stats.ErrStaleStats, nil},
		{"fresh within threshold", func() *Catalog {
			c := New()
			_ = c.AddTable("t", 105, makeTable("t", 100, "x"))
			return c
		}, nil, nil},
		{"corrupt sum", func() *Catalog {
			c := New()
			_ = c.AddTable("t", 10, makeHistTable("t", 10, true, false))
			return c
		}, nil, stats.ErrCorruptStats},
		{"corrupt bounds", func() *Catalog {
			c := New()
			_ = c.AddTable("t", 10, makeHistTable("t", 10, false, true))
			return c
		}, nil, stats.ErrCorruptStats},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			issues, err := tc.build().Validate()
			if tc.wantFatal != nil {
				if !errors.Is(err, tc.wantFatal) {
					t.Fatalf("fatal: got %v want %v", err, tc.wantFatal)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected fatal: %v", err)
			}
			if tc.wantIssue == nil {
				if len(issues) != 0 {
					t.Fatalf("unexpected issues: %+v", issues)
				}
				return
			}
			found := false
			for _, is := range issues {
				if errors.Is(is.Err, tc.wantIssue) {
					found = true
				}
			}
			if !found {
				t.Fatalf("issue %v not found in %+v", tc.wantIssue, issues)
			}
		})
	}
}

func TestPredicatesAndReads(t *testing.T) {
	c := New()
	_ = c.AddTable("a", 10, makeTable("a", 10, "x"))
	_ = c.AddTable("b", 10, makeTable("b", 10, "y"))
	cases := []struct {
		name string
		pred Predicate
		want error
	}{
		{"valid", Predicate{"a", "x", "b", "y"}, nil},
		{"self join", Predicate{"a", "x", "a", "y"}, ErrSelfJoin},
		{"duplicate", Predicate{"a", "x", "b", "y"}, nil},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := c.AddPredicate(tc.pred)
			if tc.want == nil && err != nil && i != 2 {
				t.Fatalf("unexpected: %v", err)
			}
			if i == 2 && err == nil {
				t.Fatalf("duplicate should error")
			}
			if tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatalf("got %v want %v", err, tc.want)
			}
		})
	}
	c.ReadRows("a", 3)
	c.ReadRows("a", 2)
	if got := c.RowReadCount(); got != 5 {
		t.Fatalf("row reads=%d want 5", got)
	}
	if _, _, preds := c.Snapshot(); len(preds) != 1 {
		t.Fatalf("preds=%d want 1", len(preds))
	}
}

func TestConcurrentValidate(t *testing.T) {
	c := New()
	for i := 0; i < 16; i++ {
		name := string(rune('a' + i))
		_ = c.AddTable(name, 10, makeTable(name, 10, "x"))
	}
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				if _, err := c.Validate(); err != nil {
					t.Errorf("validate: %v", err)
					return
				}
				_, _, _ = c.Snapshot()
			}
		}()
	}
	wg.Wait()
}
