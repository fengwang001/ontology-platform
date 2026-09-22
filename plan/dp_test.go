package plan

import (
	"errors"
	"fmt"
	"testing"

	"ontology/catalog"
	"ontology/cost"
	"ontology/stats"
)

type tabSpec struct {
	rows int64
	cols map[string]float64
}

func mkCatalog(t *testing.T, tabs map[string]tabSpec, preds [][4]string) *catalog.Catalog {
	t.Helper()
	c := catalog.New()
	for name, ts := range tabs {
		st := &stats.Table{Name: name, Rows: ts.rows, Columns: map[string]stats.Column{}}
		for col, ndv := range ts.cols {
			st.Columns[col] = stats.Column{NDV: ndv}
		}
		if err := c.AddTable(name, ts.rows, st); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range preds {
		if err := c.AddPredicate(p[0], p[1], p[2], p[3]); err != nil {
			t.Fatal(err)
		}
	}
	return c
}

func cartesians(n *Node) int {
	if n.Leaf() {
		return 0
	}
	c := cartesians(n.Left) + cartesians(n.Right)
	if n.Cartesian {
		c++
	}
	return c
}

func chainTabs(n int) (map[string]tabSpec, [][4]string, []string) {
	tabs := map[string]tabSpec{}
	var preds [][4]string
	var names []string
	for i := 0; i < n; i++ {
		name := fmt.Sprintf("T%02d", i)
		tabs[name] = tabSpec{rows: int64(100 * (i + 1)), cols: map[string]float64{"k": 50}}
		names = append(names, name)
		if i > 0 {
			preds = append(preds, [4]string{fmt.Sprintf("T%02d", i-1), "k", name, "k"})
		}
	}
	return tabs, preds, names
}

func TestCartesianPlacement(t *testing.T) {
	tabs, preds, names := chainTabs(4)
	t.Run("chain has no cartesian", func(t *testing.T) {
		root, _, err := Optimize(mkCatalog(t, tabs, preds), names)
		if err != nil {
			t.Fatal(err)
		}
		if c := cartesians(root); c != 0 {
			t.Fatalf("chain plan has %d cartesian joins", c)
		}
	})
	t.Run("disconnected has exactly one cartesian at the root", func(t *testing.T) {
		c := mkCatalog(t, tabs, [][4]string{{"T00", "k", "T01", "k"}, {"T02", "k", "T03", "k"}})
		root, _, err := Optimize(c, names)
		if err != nil {
			t.Fatal(err)
		}
		if !root.Cartesian {
			t.Fatal("root is not the cartesian join")
		}
		if c := cartesians(root); c != 1 {
			t.Fatalf("got %d cartesian joins, want 1", c)
		}
	})
}

func TestComplexity(t *testing.T) {
	tabs, preds, names := chainTabs(12)
	_, m, err := Optimize(mkCatalog(t, tabs, preds), names)
	if err != nil {
		t.Fatal(err)
	}
	fact := int64(1)
	for i := int64(2); i <= 12; i++ {
		fact *= i
	}
	if m.Subsets() > 1<<12 {
		t.Fatalf("subsets %d > 2^12", m.Subsets())
	}
	pow3 := int64(1)
	for i := 0; i < 12; i++ {
		pow3 *= 3
	}
	if m.Splits() > pow3 {
		t.Fatalf("splits %d > 3^12", m.Splits())
	}
	if m.Subsets()*100 > fact || m.Splits()*100 > fact {
		t.Fatalf("not far below 12!: subsets=%d splits=%d fact=%d", m.Subsets(), m.Splits(), fact)
	}
}

func TestLimitsAndEdges(t *testing.T) {
	tabs, preds, names := chainTabs(20)
	cat := mkCatalog(t, tabs, preds)
	cases := []struct {
		name  string
		tabs  []string
		want  error
		check func(*testing.T, *Node)
	}{
		{"zero tables", nil, ErrNoTables, nil},
		{"twenty tables exceed cap", names, ErrTooManyTables, nil},
		{"single table is a scan", names[:1], nil, func(t *testing.T, n *Node) {
			if !n.Leaf() || n.Cost != cost.Scan(100) || n.Card != 100 {
				t.Fatalf("got %+v", n)
			}
		}},
		{"unknown table", []string{"nope"}, catalog.ErrUnknownTable, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root, m, err := Optimize(cat, tc.tabs)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err=%v want %v", err, tc.want)
			}
			if tc.want != nil {
				if m.Subsets() != 0 || m.Splits() != 0 {
					t.Fatalf("counters moved on error: %+v", m)
				}
				return
			}
			tc.check(t, root)
		})
	}
}
