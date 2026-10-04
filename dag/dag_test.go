package dag_test

import (
	"errors"
	"fmt"
	"testing"

	"ontology/dag"
)

func TestNew(t *testing.T) {
	for _, T := range []int64{-1, 0, dag.MaxT + 1} {
		if _, err := dag.New(T); !errors.Is(err, dag.ErrInvalid) {
			t.Errorf("New(%d): want ErrInvalid, got %v", T, err)
		}
	}
	for _, T := range []int64{1, 100, dag.MaxT} {
		if _, err := dag.New(T); err != nil {
			t.Errorf("New(%d): unexpected %v", T, err)
		}
	}
}

func TestAddDatasetRejectOrder(t *testing.T) {
	mk := func(t *testing.T, freeze bool) *dag.Graph {
		t.Helper()
		g, err := dag.New(100)
		if err != nil {
			t.Fatal(err)
		}
		if err := g.AddDataset("a", 50, 10, nil); err != nil {
			t.Fatal(err)
		}
		if freeze {
			g.Freeze()
		}
		return g
	}
	cases := []struct {
		name    string
		freeze  bool
		ds      string
		off     int64
		dur     int64
		parents []string
		want    error
	}{
		{"invalid beats frozen", true, "", 0, 0, nil, dag.ErrInvalid},
		{"invalid off low", false, "b", 0, 0, nil, dag.ErrInvalid},
		{"invalid off high", false, "b", 101, 0, nil, dag.ErrInvalid},
		{"invalid dur low", false, "b", 1, -1, nil, dag.ErrInvalid},
		{"invalid dur high", false, "b", 1, 101, nil, dag.ErrInvalid},
		{"invalid too many parents", false, "b", 1, 0,
			[]string{"a", "a", "a", "a", "a", "a", "a", "a", "a"}, dag.ErrInvalid},
		{"frozen beats exists", true, "a", 50, 10, nil, dag.ErrFrozen},
		{"exists beats missing parent", false, "a", 50, 10, []string{"ghost"}, dag.ErrExists},
		{"missing parent", false, "b", 50, 10, []string{"ghost"}, dag.ErrNoSuchParent},
		{"self parent is missing parent", false, "b", 50, 10, []string{"b"}, dag.ErrNoSuchParent},
		{"ok", false, "b", 1, 100, []string{"a"}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := mk(t, tc.freeze)
			err := g.AddDataset(tc.ds, tc.off, tc.dur, tc.parents)
			if !errors.Is(err, tc.want) {
				t.Fatalf("want %v, got %v", tc.want, err)
			}
		})
	}
}

func TestAddDatasetDedupParents(t *testing.T) {
	g, _ := dag.New(100)
	if err := g.AddDataset("a", 10, 0, nil); err != nil {
		t.Fatal(err)
	}
	if err := g.AddDataset("b", 20, 0, []string{"a", "a", "a"}); err != nil {
		t.Fatal(err)
	}
	ds, ok := g.Get("b")
	if !ok || len(ds.Parents) != 1 || ds.Parents[0] != "a" {
		t.Fatalf("parents not deduped: %+v", ds)
	}
}

func TestDatasetLimit(t *testing.T) {
	g, _ := dag.New(dag.MaxT)
	for i := 0; i < dag.MaxDatasets; i++ {
		if err := g.AddDataset(fmt.Sprintf("n%d", i), 1, 0, nil); err != nil {
			t.Fatalf("add %d: %v", i, err)
		}
	}
	if err := g.AddDataset("overflow", 1, 0, nil); !errors.Is(err, dag.ErrTooMany) {
		t.Fatalf("want ErrTooMany, got %v", err)
	}
	// 超限与父不存在同时成立时，先报父不存在。
	if err := g.AddDataset("overflow2", 1, 0, []string{"ghost"}); !errors.Is(err, dag.ErrNoSuchParent) {
		t.Fatalf("want ErrNoSuchParent, got %v", err)
	}
	if g.Count() != dag.MaxDatasets {
		t.Fatalf("count=%d", g.Count())
	}
}
