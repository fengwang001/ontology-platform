package ontology_test

import (
	"errors"
	"testing"

	"ontology/attrib"
	"ontology/stack"
	"ontology/tree"
)

func TestStackNormalize(t *testing.T) {
	cases := []struct {
		name      string
		frames    []string
		max       int
		wantErr   error
		wantDepth int
		wantTrunc bool
	}{
		{"empty rejected", nil, 0, stack.ErrEmpty, 0, false},
		{"depth one", []string{"A"}, 0, nil, 1, false},
		{"empty-name frame legal", []string{"", "x"}, 0, nil, 2, false},
		{"exact limit no truncate", []string{"a", "b", "c"}, 3, nil, 3, false},
		{"limit plus one truncates", []string{"a", "b", "c", "d"}, 3, nil, 3, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, err := stack.Normalize(c.frames, c.max)
			if !errors.Is(err, c.wantErr) {
				t.Fatalf("err=%v want %v", err, c.wantErr)
			}
			if err == nil && (s.Depth() != c.wantDepth || s.Truncated != c.wantTrunc) {
				t.Fatalf("depth=%d trunc=%v want %d/%v", s.Depth(), s.Truncated, c.wantDepth, c.wantTrunc)
			}
		})
	}
	got := stack.Dedup([]string{"A", "A", "B", "A"})
	if len(got) != 3 || got[0] != "A" || got[1] != "B" || got[2] != "A" {
		t.Fatalf("dedup=%v", got)
	}
}

func makeDeep(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = "d"
	}
	return out
}

func TestTreeIdentitiesAndEdges(t *testing.T) {
	cases := []struct {
		name   string
		stacks [][]string
		max    int
		trunc  uint64
	}{
		{"mixed", [][]string{{"m", "A", "B"}, {"m", "A", "C"}, {"m", "D"}}, 0, 0},
		{"single sample", [][]string{{"m", "A"}}, 0, 0},
		{"all same stack", [][]string{{"m", "A"}, {"m", "A"}, {"m", "A"}}, 0, 0},
		{"depth one", [][]string{{"X"}, {"Y"}, {"X"}}, 0, 0},
		{"truncation", [][]string{makeDeep(12)}, 8, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tr := tree.New()
			for _, f := range c.stacks {
				s, err := stack.Normalize(f, c.max)
				if err != nil {
					t.Fatal(err)
				}
				tr.Insert(s)
			}
			if tr.SumSelf() != tr.Samples {
				t.Fatalf("Σself=%d != samples=%d", tr.SumSelf(), tr.Samples)
			}
			if tr.SumTotal() <= tr.Samples {
				t.Fatalf("Σtotal=%d must exceed samples=%d", tr.SumTotal(), tr.Samples)
			}
			if tr.TruncatedSamples != c.trunc {
				t.Fatalf("truncated=%d want %d", tr.TruncatedSamples, c.trunc)
			}
		})
	}
	rec := tree.New()
	for i := 0; i < 7; i++ {
		s, _ := stack.Normalize([]string{"A", "F", "G", "F", "H"}, 0)
		rec.Insert(s)
	}
	for _, h := range attrib.ByTotal(rec) {
		if h.Frame == "F" && (h.Total != 7 || h.Self != 0) {
			t.Fatalf("FuncTotal(F)=%d want 7; self=%d", h.Total, h.Self)
		}
	}
}

func TestInsertComplexity(t *testing.T) {
	tr := tree.New()
	base := make([]string, 20)
	for i := 0; i < 19; i++ {
		base[i] = "p"
	}
	for i := 0; i < 100000; i++ {
		base[19] = []string{"a", "b"}[i&1]
		s, _ := stack.Normalize(base, 0)
		tr.Insert(s)
	}
	if got, bound := tr.Lookups(), uint64(100000*20*4); got > bound {
		t.Fatalf("lookups=%d > bound=%d", got, bound)
	}
}

func TestAttribution(t *testing.T) {
	if hs := attrib.ByTotal(tree.New()); len(hs) != 0 {
		t.Fatalf("zero samples must give empty attribution, got %d", len(hs))
	}
	tr := tree.New()
	for _, f := range [][]string{{"m", "F", "x"}, {"m", "F", "y"}, {"m", "G", "z"}} {
		s, _ := stack.Normalize(f, 0)
		tr.Insert(s)
	}
	bySelf := attrib.BySelf(tr)
	byTotal := attrib.ByTotal(tr)
	if attrib.Rebuilds() != 0 {
		t.Fatalf("sort queries rebuilt tree %d times", attrib.Rebuilds())
	}
	if bySelf[0].Frame != "x" || byTotal[0].Frame != "m" {
		t.Fatalf("top self=%s top total=%s", bySelf[0].Frame, byTotal[0].Frame)
	}
	ex := attrib.Exclude(tr, "F")
	if ex.SumSelf() != tr.Samples {
		t.Fatalf("excluded self-sum=%d want %d", ex.SumSelf(), tr.Samples)
	}
	promoted := false
	ex.Walk(func(n *tree.Node, d int) {
		if n.Frame == "x" && d == 2 {
			promoted = true
		}
	})
	if !promoted {
		t.Fatal("children of excluded F were not promoted")
	}
}
