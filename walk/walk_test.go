package walk

import (
	"bytes"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"

	"ontology/graph"
	"ontology/resume"
)

func edgesToGraph(pairs ...[2]string) *graph.Graph {
	g := graph.New()
	for _, e := range pairs {
		g.AddEdge(e[0], e[1])
	}
	return g
}

// diamondCycle: a->b,c; b->d; c->d; d->e; e->b (cycle back). Reachable: 5.
func diamondCycle() *graph.Graph {
	return edgesToGraph([2]string{"a", "b"}, [2]string{"a", "c"}, [2]string{"b", "d"},
		[2]string{"c", "d"}, [2]string{"d", "e"}, [2]string{"e", "b"})
}

func mustWalk(t *testing.T, g *graph.Graph, start string, budget int) Result {
	t.Helper()
	r, err := Walk(g, start, budget)
	if err != nil {
		t.Fatalf("Walk(%q,%d): %v", start, budget, err)
	}
	return r
}

func TestDeterministicRepeat(t *testing.T) {
	g := diamondCycle()
	want := mustWalk(t, g, "a", 4).Seq
	for i := 0; i < 20; i++ {
		got := mustWalk(t, g, "a", 4)
		if !slices.Equal(got.Seq, want) {
			t.Fatalf("run %d: seq %v != %v", i, got.Seq, want)
		}
		if !bytes.Equal(got.Token, mustWalk(t, g, "a", 4).Token) {
			t.Fatalf("run %d: token not deterministic", i)
		}
	}
}

func TestBudgetZeroIdempotent(t *testing.T) {
	g := diamondCycle()
	tok0, err := InitialToken(g, "a")
	if err != nil {
		t.Fatal(err)
	}
	r := mustWalk(t, g, "a", 0)
	if len(r.Seq) != 0 || !bytes.Equal(r.Token, tok0) {
		t.Fatalf("budget 0: seq=%v token changed=%v", r.Seq, !bytes.Equal(r.Token, tok0))
	}
	r2, err := Resume(g, tok0, 0)
	if err != nil || len(r2.Seq) != 0 || !bytes.Equal(r2.Token, tok0) {
		t.Fatalf("resume budget 0: seq=%v err=%v", r2.Seq, err)
	}
}

func TestDoneTokenResumeEmpty(t *testing.T) {
	g := diamondCycle()
	full := mustWalk(t, g, "a", 100)
	st, err := resume.Decode(g, full.Token)
	if err != nil || !st.Done {
		t.Fatalf("full traversal must end done: %+v err=%v", st, err)
	}
	r, err := Resume(g, full.Token, 5)
	if err != nil || len(r.Seq) != 0 || !bytes.Equal(r.Token, full.Token) {
		t.Fatalf("resume done token: seq=%v err=%v", r.Seq, err)
	}
}

func TestVisitedEqualsMinBudgetReachable(t *testing.T) {
	g := diamondCycle()
	const reachable = 5
	for budget := 0; budget <= reachable+2; budget++ {
		r := mustWalk(t, g, "a", budget)
		if want := min(budget, reachable); r.Stats.Visited != want || len(r.Seq) != want {
			t.Fatalf("budget %d: visited=%d len=%d want=%d", budget, r.Stats.Visited, len(r.Seq), want)
		}
	}
}

func star(leaves int) *graph.Graph {
	g := graph.New()
	for i := 0; i < leaves; i++ {
		g.AddEdge("hub", fmt.Sprintf("leaf%05d", i))
	}
	return g
}

func TestStarPeakQueue(t *testing.T) {
	g := star(10000)
	for _, budget := range []int{10, 100, 1000} {
		r := mustWalk(t, g, "hub", budget)
		if r.Stats.Visited != budget {
			t.Fatalf("budget %d: visited=%d", budget, r.Stats.Visited)
		}
		if r.Stats.PeakQueue > 4*budget {
			t.Fatalf("budget %d: peak queue %d exceeds %d", budget, r.Stats.PeakQueue, 4*budget)
		}
		t.Logf("budget=%d visited=%d peak=%d bound=%d", budget, r.Stats.Visited, r.Stats.PeakQueue, 4*budget)
	}
}

func TestEdgesExaminedBound(t *testing.T) {
	g := diamondCycle()
	for budget := 1; budget <= 5; budget++ {
		r := mustWalk(t, g, "a", budget)
		bound := 0
		for _, id := range r.Seq {
			bound += g.OutDegree(id)
		}
		if r.Stats.EdgesExamined > bound {
			t.Fatalf("budget %d: edges %d > out-degree sum %d", budget, r.Stats.EdgesExamined, bound)
		}
	}
}

func TestGraphShapes(t *testing.T) {
	cases := []struct {
		name   string
		g      *graph.Graph
		start  string
		budget int
		want   []string
	}{
		{"cycle", edgesToGraph([2]string{"a", "b"}, [2]string{"b", "c"}, [2]string{"c", "a"}),
			"a", 10, []string{"a", "b", "c"}},
		{"self loop", edgesToGraph([2]string{"a", "a"}, [2]string{"a", "b"}),
			"a", 10, []string{"a", "b"}},
		{"duplicate edges", edgesToGraph([2]string{"a", "b"}, [2]string{"a", "b"}),
			"a", 10, []string{"a", "b"}},
		{"unreachable ignored", edgesToGraph([2]string{"a", "b"}, [2]string{"c", "d"}),
			"a", 10, []string{"a", "b"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := mustWalk(t, tc.g, tc.start, tc.budget)
			if !slices.Equal(r.Seq, tc.want) {
				t.Fatalf("seq=%v want %v", r.Seq, tc.want)
			}
		})
	}
}

func TestMissingStart(t *testing.T) {
	for _, g := range []*graph.Graph{graph.New(), diamondCycle()} {
		if _, err := Walk(g, "ghost", 5); !errors.Is(err, ErrStartMissing) {
			t.Fatalf("err=%v, want ErrStartMissing", err)
		}
	}
}

func TestMutationBetweenSegments(t *testing.T) {
	g := diamondCycle()
	first := mustWalk(t, g, "a", 2) // queue still holds b
	g.Remove("b")
	_, err := Resume(g, first.Token, 3)
	if !errors.Is(err, resume.ErrUnknownNode) {
		t.Fatalf("err=%v, want ErrUnknownNode", err)
	}
	if !strings.Contains(err.Error(), `"b"`) {
		t.Fatalf("error must name the removed node: %v", err)
	}
}

func TestConcurrentResume(t *testing.T) {
	g := diamondCycle()
	first := mustWalk(t, g, "a", 2)
	want := mustWalk(t, g, "a", 5).Seq[2:]
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := Resume(g, first.Token, 3)
			if err != nil || !slices.Equal(r.Seq, want) {
				t.Errorf("seq=%v err=%v, want %v", r.Seq, err, want)
			}
		}()
	}
	wg.Wait()
}
