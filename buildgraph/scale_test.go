package buildgraph

import (
	"fmt"
	"sort"
	"sync"
	"testing"
)

func buildChain(t *testing.T, g *Graph, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		var inputs []string
		if i == 0 {
			inputs = []string{"src"}
		} else {
			inputs = []string{fmt.Sprintf("n%d.out", i-1)}
		}
		mustAdd(t, g, fmt.Sprintf("n%d", i), "c", []string{fmt.Sprintf("n%d.out", i)}, inputs, nil, nil, i%3 == 0)
	}
}

func completeChain(t *testing.T, g *Graph, n int) {
	t.Helper()
	mustSet(t, g, "src", 1)
	for i := 0; i < n; i++ {
		out := fmt.Sprintf("n%d.out", i)
		if err := g.Complete(fmt.Sprintf("n%d", i), map[string]int64{out: int64(i + 2)}); err != nil {
			t.Fatalf("complete n%d: %v", i, err)
		}
	}
}

func TestDeepChainNoStackOverflow(t *testing.T) {
	const n = 20000
	g := New()
	buildChain(t, g, n)
	completeChain(t, g, n)
	target := fmt.Sprintf("n%d.out", n-1)
	if got := dirtySet(t, g, []string{target}); len(got) != 0 {
		t.Fatalf("clean chain expected no dirty edges, got %d (%v...)", len(got), got[:min(5, len(got))])
	}
	if c := g.EvalCount(); c != n {
		t.Fatalf("eval count = %d, want %d", c, n)
	}
	// Dirty the source: every edge is dirty, each evaluated once.
	mustSet(t, g, "src", 100)
	got := dirtySet(t, g, []string{target})
	if len(got) != n {
		t.Fatalf("dirty edges = %d, want %d", len(got), n)
	}
	if c := g.EvalCount(); c != n {
		t.Fatalf("eval count after dirty = %d, want %d", c, n)
	}
	// Naive, memo-free evaluation must agree (kept short here; depth 20000
	// would be exponential for diamonds but is linear for a chain).
	naive, err := g.NaiveDirtySet([]string{target})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range got {
		if naive[id] == "" {
			t.Fatalf("naive says %s clean but implementation says dirty", id)
		}
	}
}

// buildDiamonds builds `layers` layers; each layer has width edges, every one
// consuming every output of the previous layer (heavy shared-diamond fan-in).
func buildDiamonds(t *testing.T, g *Graph, layers, width int) []string {
	t.Helper()
	prev := []string{"s0", "s1"}
	for _, p := range prev {
		mustSet(t, g, p, 1)
	}
	var last []string
	for l := 0; l < layers; l++ {
		var cur []string
		for w := 0; w < width; w++ {
			id := fmt.Sprintf("L%d-W%d", l, w)
			out := id + ".out"
			mustAdd(t, g, id, fmt.Sprintf("cmd-%d-%d", l, w), []string{out}, prev, nil, []string{"s1"}, l%2 == 0)
			cur = append(cur, out)
		}
		prev = cur
		last = cur
	}
	return last
}

func TestDiamondEvalCount(t *testing.T) {
	for _, layers := range []int{100, 200} {
		width := 10
		n := layers * width
		t.Run(fmt.Sprintf("edges=%d", n), func(t *testing.T) {
			g := New()
			last := buildDiamonds(t, g, layers, width)
			// Complete in layer order.
			for l := 0; l < layers; l++ {
				for w := 0; w < width; w++ {
					id := fmt.Sprintf("L%d-W%d", l, w)
					if err := g.Complete(id, map[string]int64{id + ".out": int64(l + 2)}); err != nil {
						t.Fatal(err)
					}
				}
			}
			if got := dirtySet(t, g, last); len(got) != 0 {
				t.Fatalf("expected clean, got %d dirty", len(got))
			}
			if c := g.EvalCount(); c != n {
				t.Fatalf("eval count = %d, want closure size %d", c, n)
			}
		})
	}
}

func TestDiamondEvalCount20000(t *testing.T) {
	// 2000 edges would not suffice; target exactly 20000 edges with shared diamonds:
	// 200 layers x 100 width.
	const layers, width = 200, 100
	const n = layers * width
	g := New()
	last := buildDiamonds(t, g, layers, width)
	for l := 0; l < layers; l++ {
		for w := 0; w < width; w++ {
			id := fmt.Sprintf("L%d-W%d", l, w)
			if err := g.Complete(id, map[string]int64{id + ".out": int64(l + 2)}); err != nil {
				t.Fatal(err)
			}
		}
	}
	if got := dirtySet(t, g, last); len(got) != 0 {
		t.Fatalf("expected clean, got %d dirty", len(got))
	}
	if c := g.EvalCount(); c != n {
		t.Fatalf("eval count = %d, want %d", c, n)
	}
	mustSet(t, g, "s0", 50)
	got := dirtySet(t, g, last)
	if len(got) == 0 {
		t.Fatal("expected dirty fan-out")
	}
	if c := g.EvalCount(); c != n {
		t.Fatalf("dirty eval count = %d, want %d", c, n)
	}
	// Sanity: results are byte-sorted.
	if !sort.StringsAreSorted(got) {
		t.Fatal("dirty set not sorted by byte order")
	}
}

func TestConcurrentOps(t *testing.T) {
	g := New()
	mustAdd(t, g, "e", "c", []string{"e.out"}, []string{"src"}, nil, nil, false)
	mustSet(t, g, "src", 1)
	if err := g.Complete("e", map[string]int64{"e.out": 2}); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			p := fmt.Sprintf("file-%d", i)
			for k := 0; k < 200; k++ {
				if err := g.SetMtime(p, int64(k+1)); err != nil {
					t.Errorf("set: %v", err)
					return
				}
				if _, err := g.DirtySet([]string{"e.out"}); err != nil {
					t.Errorf("dirtyset: %v", err)
					return
				}
				if err := g.Remove(p); err != nil {
					t.Errorf("remove: %v", err)
					return
				}
			}
		}(i)
	}
	wg.Wait()
}

func TestReplayDeterminism(t *testing.T) {
	g1 := New()
	buildChain(t, g1, 50)
	completeChain(t, g1, 50)
	mustSet(t, g1, "src", 77)

	g2 := New()
	buildChain(t, g2, 50)
	completeChain(t, g2, 50)
	mustSet(t, g2, "src", 77)

	r1, err := g1.DirtySet([]string{"n49.out"})
	if err != nil {
		t.Fatal(err)
	}
	r2, err := g2.DirtySet([]string{"n49.out"})
	if err != nil {
		t.Fatal(err)
	}
	if len(r1) != len(r2) {
		t.Fatalf("replay mismatch %d vs %d", len(r1), len(r2))
	}
	for i := range r1 {
		if r1[i] != r2[i] {
			t.Fatalf("replay mismatch at %d: %s vs %s", i, r1[i], r2[i])
		}
	}
}
