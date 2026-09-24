package tree_test

import (
	"errors"
	"fmt"
	"testing"

	"ontology/attrib"
	"ontology/stack"
	"ontology/tree"
)

func insertN(tr *tree.Tree, n int, frames ...string) {
	for i := 0; i < n; i++ {
		tr.Insert(frames, false)
	}
}

func TestNormalize(t *testing.T) {
	cases := []struct {
		name     string
		frames   []string
		maxDepth int
		want     []string
		trunc    bool
		wantErr  error
	}{
		{"empty rejected", nil, 8, nil, false, stack.ErrEmpty},
		{"dedup consecutive", []string{"a", "a", "b", "b", "b", "c"}, 8, []string{"a", "b", "c"}, false, nil},
		{"non-consecutive kept", []string{"a", "b", "a"}, 8, []string{"a", "b", "a"}, false, nil},
		{"empty frame name legal", []string{"", "x"}, 8, []string{"", "x"}, false, nil},
		{"depth == max no truncation", []string{"a", "b", "c"}, 3, []string{"a", "b", "c"}, false, nil},
		{"depth max+1 truncated", []string{"a", "b", "c", "d"}, 3, []string{"a", "b", "c"}, true, nil},
		{"depth 1", []string{"a"}, 3, []string{"a"}, false, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, trunc, err := stack.Normalize(tc.frames, tc.maxDepth)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err=%v want %v", err, tc.wantErr)
			}
			if trunc != tc.trunc || fmt.Sprint(got) != fmt.Sprint(tc.want) {
				t.Fatalf("got %v trunc=%v, want %v trunc=%v", got, trunc, tc.want, tc.trunc)
			}
		})
	}
}

func TestIdentities(t *testing.T) {
	cases := []struct {
		name      string
		stacks    [][]string
		repeat    int
		wantSelf  int64
		wantTotal int64
	}{
		{"zero samples", nil, 0, 0, 0},
		{"single sample", [][]string{{"a", "b"}}, 1, 1, 3},
		{"all same stack", [][]string{{"a", "b", "c"}}, 10, 10, 40},
		{"depth 1 stacks", [][]string{{"a"}, {"b"}}, 3, 6, 12},
		{"mixed depths", [][]string{{"a"}, {"a", "b"}, {"a", "b", "c"}}, 1, 3, 9},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tr := tree.New()
			for i := 0; i < tc.repeat; i++ {
				for _, s := range tc.stacks {
					tr.Insert(s, false)
				}
			}
			selfSum, totalSum := tree.SumSelf(tr.Root), tree.SumTotal(tr.Root)
			if selfSum != tr.Samples || selfSum != tc.wantSelf {
				t.Fatalf("self-sum=%d samples=%d want %d", selfSum, tr.Samples, tc.wantSelf)
			}
			if totalSum != tc.wantTotal {
				t.Fatalf("total-sum=%d want %d", totalSum, tc.wantTotal)
			}
			if tr.Samples > 0 && totalSum <= selfSum {
				t.Fatalf("total-sum %d must exceed self-sum %d", totalSum, selfSum)
			}
		})
	}
}

func TestTruncation(t *testing.T) {
	tr := tree.New()
	deep := make([]string, 9)
	for i := range deep {
		deep[i] = fmt.Sprintf("d%d", i)
	}
	norm, trunc, _ := stack.Normalize(deep, 8)
	tr.Insert(norm, trunc)
	if tr.TruncatedSamples != 1 || tr.Samples != 1 {
		t.Fatalf("truncated=%d samples=%d", tr.TruncatedSamples, tr.Samples)
	}
	node := tr.Root
	for _, f := range norm {
		node = node.Children[f]
	}
	if !node.Truncated {
		t.Fatal("truncation point node not marked")
	}
	if tree.SumSelf(tr.Root) != 1 {
		t.Fatal("truncated sample must still be counted")
	}
}

func TestInsertCost(t *testing.T) {
	tr := tree.New()
	frames := make([]string, 20)
	for i := range frames {
		frames[i] = fmt.Sprintf("f%d", i)
	}
	const n = 100000
	for i := 0; i < n; i++ {
		tr.Insert(frames, false)
	}
	if got, bound := tr.Lookups(), int64(n*20*4); got <= 0 || got > bound {
		t.Fatalf("lookups=%d exceeds bound %d", got, bound)
	}
}

func TestRecursion(t *testing.T) {
	tr := tree.New()
	insertN(tr, 5, "A", "F", "G", "F", "H")
	insertN(tr, 2, "A", "F", "G")
	want := []struct {
		path      []string
		self, tot int64
	}{
		{[]string{"A"}, 0, 7}, {[]string{"A", "F"}, 0, 7}, {[]string{"A", "F", "G"}, 2, 7},
		{[]string{"A", "F", "G", "F"}, 0, 5}, {[]string{"A", "F", "G", "F", "H"}, 5, 5},
	}
	for _, w := range want {
		node := tr.Root
		for _, f := range w.path {
			node = node.Children[f]
		}
		if node.Self != w.self || node.Total != w.tot {
			t.Fatalf("%v: self=%d total=%d want %d/%d", w.path, node.Self, node.Total, w.self, w.tot)
		}
	}
	ft := attrib.FunctionTotals(tr)
	if ft["F"] != 7 {
		t.Fatalf("F function total=%d, want outermost 7 (not 12)", ft["F"])
	}
	for f, wantTot := range map[string]int64{"A": 7, "G": 7, "H": 5} {
		if ft[f] != wantTot {
			t.Fatalf("%s function total=%d want %d", f, ft[f], wantTot)
		}
	}
}

func TestAttrib(t *testing.T) {
	buildTree := func() *tree.Tree {
		tr := tree.New()
		insertN(tr, 3, "a", "b", "c")
		insertN(tr, 2, "a", "b", "d")
		insertN(tr, 4, "e")
		return tr
	}
	cases := []struct {
		name  string
		build func() *tree.Tree
		query func(*tree.Tree) []attrib.Entry
		want  []attrib.Entry
	}{
		{"zero samples", tree.New, attrib.BySelf, nil},
		{"by self", buildTree, attrib.BySelf, []attrib.Entry{
			{Frame: "e", Self: 4, Total: 4}, {Frame: "c", Self: 3, Total: 3},
			{Frame: "d", Self: 2, Total: 2}, {Frame: "a", Self: 0, Total: 5}, {Frame: "b", Self: 0, Total: 5},
		}},
		{"by total", buildTree, attrib.ByTotal, []attrib.Entry{
			{Frame: "a", Self: 0, Total: 5}, {Frame: "b", Self: 0, Total: 5},
			{Frame: "e", Self: 4, Total: 4}, {Frame: "c", Self: 3, Total: 3}, {Frame: "d", Self: 2, Total: 2},
		}},
		{"excluding charges caller", func() *tree.Tree {
			tr := tree.New()
			insertN(tr, 4, "a", "b")
			insertN(tr, 1, "a", "b", "c")
			return tr
		}, func(tr *tree.Tree) []attrib.Entry { return attrib.Excluding(tr, "b") }, []attrib.Entry{
			{Frame: "a", Self: 4, Total: 5}, {Frame: "c", Self: 1, Total: 1},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tr := tc.build()
			got := tc.query(tr)
			if fmt.Sprint(got) != fmt.Sprint(tc.want) {
				t.Fatalf("got %+v want %+v", got, tc.want)
			}
			if tr.Rebuilds() != 0 {
				t.Fatal("attribution must not rebuild the tree")
			}
		})
	}
}
