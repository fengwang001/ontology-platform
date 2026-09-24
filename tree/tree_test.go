package tree_test

import (
	"fmt"
	"sync"
	"testing"

	"ontology/attrib"
	"ontology/stack"
	"ontology/tree"
)

func insert(t *testing.T, tr *tree.Tree, frames []string, maxDepth int) {
	t.Helper()
	s, err := stack.Normalize(frames, maxDepth)
	if err != nil {
		t.Fatal(err)
	}
	tr.Insert(s)
}

func TestNormalize(t *testing.T) {
	cases := []struct {
		name      string
		frames    []string
		maxDepth  int
		want      []string
		truncated bool
		wantErr   error
	}{
		{"empty rejected", nil, 64, nil, false, stack.ErrEmpty},
		{"consecutive dedup", []string{"a", "a", "b", "b", "b"}, 64, []string{"a", "b"}, false, nil},
		{"recursion kept", []string{"A", "F", "G", "F", "H"}, 64, []string{"A", "F", "G", "F", "H"}, false, nil},
		{"empty frame legal", []string{""}, 64, []string{""}, false, nil},
		{"depth one", []string{"main"}, 64, []string{"main"}, false, nil},
		{"depth==max kept", []string{"a", "b", "c"}, 3, []string{"a", "b", "c"}, false, nil},
		{"depth max+1 cut", []string{"a", "b", "c", "d"}, 3, []string{"a", "b", "c"}, true, nil},
		{"dedup before limit", []string{"a", "a", "a", "b"}, 2, []string{"a", "b"}, false, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, err := stack.Normalize(tc.frames, tc.maxDepth)
			if err != tc.wantErr {
				t.Fatalf("err=%v want %v", err, tc.wantErr)
			}
			if tc.wantErr != nil {
				return
			}
			if s.Truncated != tc.truncated || fmt.Sprint(s.Frames) != fmt.Sprint(tc.want) {
				t.Fatalf("got %v truncated=%v", s.Frames, s.Truncated)
			}
		})
	}
}

func recursionTree(t *testing.T) *tree.Tree {
	t.Helper()
	tr := tree.New()
	insert(t, tr, []string{"A", "F", "G", "F", "H"}, 64)
	insert(t, tr, []string{"A", "F", "G"}, 64)
	return tr
}

func TestSelfTotalIdentities(t *testing.T) {
	tr := recursionTree(t)
	insert(t, tr, []string{"B"}, 64)
	snap := tr.Snapshot()
	if snap.SelfSum() != tr.Samples() {
		t.Fatalf("identity A: self-sum %d != samples %d", snap.SelfSum(), tr.Samples())
	}
	if snap.TotalSum() <= tr.Samples() {
		t.Fatalf("identity B: total-sum %d should exceed samples %d", snap.TotalSum(), tr.Samples())
	}
	var check func(n *tree.SNode)
	check = func(n *tree.SNode) {
		var kids int64
		for _, c := range n.Children {
			kids += c.Total
			check(c)
		}
		if n.Total != n.Self+kids {
			t.Fatalf("node %q: total %d != self %d + children %d", n.Frame, n.Total, n.Self, kids)
		}
	}
	check(snap.Root)
}

func TestAttrib(t *testing.T) {
	rep := attrib.NewReport(recursionTree(t).Snapshot())
	cases := []struct {
		name  string
		frame string
		want  int64
	}{
		{"recursive F outermost only", "F", 2}, // not 2+1=3
		{"G plain", "G", 2},
		{"A root of all", "A", 2},
		{"leaf H", "H", 1},
		{"absent frame", "ZZ", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := rep.FunctionTotal(tc.frame); got != tc.want {
				t.Fatalf("FunctionTotal(%q)=%d want %d", tc.frame, got, tc.want)
			}
		})
	}
	bySelf := rep.BySelf()
	if len(bySelf) == 0 || bySelf[0].Frame != "G" && bySelf[0].Frame != "H" {
		t.Fatalf("BySelf top = %+v", bySelf)
	}
	var selfSum int64
	for _, e := range bySelf {
		selfSum += e.Self
	}
	if selfSum != rep.Samples() {
		t.Fatalf("function self-sum %d != samples %d", selfSum, rep.Samples())
	}
	ex := rep.Excluding("G") // G self folds into outer F; totals unchanged
	got := map[string]attrib.Entry{}
	for _, e := range ex {
		got[e.Frame] = e
	}
	if got["F"].Self != 1 || got["F"].Total != 2 || got["H"].Total != 1 {
		t.Fatalf("Excluding(G) = %+v", got)
	}
	if _, ok := got["G"]; ok {
		t.Fatalf("excluded frame still present: %+v", got["G"])
	}
	for i := 0; i < 5; i++ {
		rep.BySelf()
		rep.ByTotal()
		rep.Excluding("F")
	}
	if rep.Rebuilds() != 0 {
		t.Fatalf("attribution rebuilt the tree %d times", rep.Rebuilds())
	}
	if empty := attrib.NewReport(tree.New().Snapshot()); len(empty.BySelf()) != 0 || empty.Samples() != 0 {
		t.Fatal("zero samples must attribute to empty, not error")
	}
}

func TestInsertCostBound(t *testing.T) {
	tr := tree.New()
	const inserts, depth = 100000, 20
	frames := make([]string, depth)
	for i := range frames {
		frames[i] = fmt.Sprintf("f%d", i)
	}
	insert(t, tr, frames, 64)
	ops1 := tr.InsertOps()
	for i := 1; i < inserts; i++ {
		insert(t, tr, frames, 64)
	}
	ops := tr.InsertOps()
	if ops1 > depth*4 || ops > inserts*depth*4 {
		t.Fatalf("insert ops %d exceed bound %d", ops, inserts*depth*4)
	}
	if ops < inserts*depth {
		t.Fatalf("insert ops %d below one lookup per frame %d", ops, inserts*depth)
	}
}

func TestConcurrentQuery(t *testing.T) {
	tr := tree.New()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 2000; i++ {
			s, _ := stack.Normalize([]string{"a", "b", fmt.Sprintf("c%d", i%7)}, 64)
			tr.Insert(s)
		}
	}()
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				snap := tr.Snapshot()
				if snap.SelfSum() != snap.Samples {
					t.Errorf("half-updated tree: self-sum %d != samples %d", snap.SelfSum(), snap.Samples)
					return
				}
				attrib.NewReport(snap).ByTotal()
			}
		}()
	}
	wg.Wait()
	if tr.Samples() != 2000 {
		t.Fatalf("samples=%d", tr.Samples())
	}
}
