package tree

import (
	"testing"

	"ontology/stack"
)

func f(name string) stack.Frame { return stack.Frame{Func: name} }

func sums(n *Node) (self, total int) {
	self += n.Self
	total += n.Total
	for _, c := range n.Children {
		s, tv := sums(c)
		self += s
		total += tv
	}
	return self, total
}

func TestInsertAggregates(t *testing.T) {
	type insert struct {
		frames []stack.Frame
		depth  int
	}
	cases := []struct {
		name        string
		inserts     []insert
		wantSamples int
		wantTrunc   int
		wantInvalid int
	}{
		{"zero", nil, 0, 0, 0},
		{"single", []insert{{[]stack.Frame{f("A")}, 8}}, 1, 0, 0},
		{"same-stack-twice", []insert{
			{[]stack.Frame{f("A"), f("B")}, 8},
			{[]stack.Frame{f("A"), f("B")}, 8},
		}, 2, 0, 0},
		{"empty-is-invalid", []insert{{nil, 8}}, 0, 0, 1},
		{"truncated-counted", []insert{{[]stack.Frame{f("A"), f("B"), f("C")}, 2}}, 1, 1, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tr := New()
			for _, in := range tc.inserts {
				tr.Insert(stack.Normalize(in.frames, in.depth))
			}
			s, trunc, inv, _ := tr.Stats()
			if s != tc.wantSamples || trunc != tc.wantTrunc || inv != tc.wantInvalid {
				t.Fatalf("stats=(%d,%d,%d) want (%d,%d,%d)",
					s, trunc, inv, tc.wantSamples, tc.wantTrunc, tc.wantInvalid)
			}
			self, total := sums(tr.Snapshot())
			if self != s {
				t.Fatalf("Σself=%d want samples=%d", self, s)
			}
			if total < self {
				t.Fatalf("Σtotal=%d must be >= Σself=%d", total, self)
			}
		})
	}
}

func TestTotalSumExceedsSamples(t *testing.T) {
	tr := New()
	tr.Insert(stack.Normalize([]stack.Frame{f("A"), f("B"), f("C")}, 8))
	_, total := sums(tr.Snapshot())
	if total != 3 {
		t.Fatalf("Σtotal=%d want 3", total)
	}
}

func TestRecursionProducesTwoNodes(t *testing.T) {
	tr := New()
	tr.Insert(stack.Normalize([]stack.Frame{f("A"), f("F"), f("G"), f("F"), f("H")}, 8))
	root := tr.Snapshot()
	var fNodes int
	var walk func(*Node)
	walk = func(n *Node) {
		if n.Frame.Func == "F" {
			fNodes++
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(root)
	if fNodes != 2 {
		t.Fatalf("F nodes=%d want 2", fNodes)
	}
}

func TestInsertLookupBound(t *testing.T) {
	tr := New()
	const n, depth = 100000, 20
	stk := make([]stack.Frame, depth)
	for i := range stk {
		stk[i] = f("f" + string(rune('a'+i)))
	}
	r := stack.Normalize(stk, 64)
	for i := 0; i < n; i++ {
		tr.Insert(r)
	}
	_, _, _, lookups := tr.Stats()
	if lookups > n*depth*4 {
		t.Fatalf("lookups=%d exceed bound %d", lookups, n*depth*4)
	}
}
