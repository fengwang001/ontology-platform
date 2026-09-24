package walk

import (
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"

	"ontology/graph"
)

func buildGraph(t *testing.T, edges [][2]string, isolated ...string) *graph.Graph {
	t.Helper()
	b := graph.NewBuilder()
	for _, e := range edges {
		b.AddEdge(e[0], e[1])
	}
	for _, id := range isolated {
		b.AddNode(id)
	}
	return b.Build()
}

func fullOrder(g *graph.Graph, start string) []string {
	st := Initial(start)
	out, fin, err := Run(g, st, 1<<30)
	if err != nil {
		panic(err)
	}
	if !fin.Done {
		panic("not done")
	}
	return out
}

func TestDeterminism(t *testing.T) {
	g := buildGraph(t, [][2]string{
		{"s", "b"}, {"s", "a"}, {"s", "s"},
		{"a", "c"}, {"b", "c"}, {"c", "s"}, {"a", "a"},
	}, "u")
	for _, budget := range []int{1, 2, 3, 4, 100} {
		var ref []string
		for i := 0; i < 20; i++ {
			out, _, err := Run(g, Initial("s"), budget)
			if err != nil {
				t.Fatal(err)
			}
			if i == 0 {
				ref = out
			} else if !reflect.DeepEqual(out, ref) {
				t.Fatalf("budget %d run %d: %v != %v", budget, i, out, ref)
			}
		}
	}
	if got := fullOrder(g, "s"); !reflect.DeepEqual(got, []string{"s", "a", "b", "c"}) {
		t.Fatalf("order = %v", got)
	}
}

func TestBudgetPrecise(t *testing.T) {
	g := buildGraph(t, [][2]string{{"s", "a"}, {"a", "b"}, {"b", "c"}}, "u")
	reachable := 4
	for budget := 0; budget <= 7; budget++ {
		out, fin, err := Run(g, Initial("s"), budget)
		if err != nil {
			t.Fatal(err)
		}
		want := min(budget, reachable)
		if len(out) != want || fin.CountVisited() != want {
			t.Fatalf("budget %d: visited %d/%d, want %d", budget, len(out), fin.CountVisited(), want)
		}
	}
}

func TestSplitEquivalence(t *testing.T) {
	g := buildGraph(t, [][2]string{
		{"s", "d"}, {"s", "a"}, {"a", "c"}, {"a", "b"},
		{"b", "s"}, {"c", "d"}, {"d", "e"}, {"e", "e"},
	}, "u")
	oneShot := fullOrder(g, "s")
	n := len(oneShot)
	if !reflect.DeepEqual(oneShot, []string{"s", "a", "d", "b", "c", "e"}) {
		t.Fatalf("reachable = %d", n)
	}
	for a := 1; a < n; a++ {
		first, st, err := Run(g, Initial("s"), a)
		if err != nil {
			t.Fatal(err)
		}
		second, fin, err := Run(g, st, n-a)
		if err != nil {
			t.Fatal(err)
		}
		joined := append(append([]string{}, first...), second...)
		if !reflect.DeepEqual(joined, oneShot) {
			t.Fatalf("split %d/%d: %v != %v", a, n-a, joined, oneShot)
		}
		if !fin.Done {
			t.Fatalf("split %d: final not done", a)
		}
	}
}

func TestZeroAndDone(t *testing.T) {
	g := buildGraph(t, [][2]string{{"s", "a"}})
	out, fin, err := Run(g, Initial("s"), 0)
	if err != nil || len(out) != 0 || fin.Done {
		t.Fatalf("zero budget: out=%v done=%v err=%v", out, fin.Done, err)
	}
	if !reflect.DeepEqual(fin, Initial("s")) {
		t.Fatalf("zero budget state != initial")
	}
	all, done, err := Run(g, Initial("s"), 100)
	if err != nil || !reflect.DeepEqual(all, []string{"s", "a"}) || !done.Done {
		t.Fatalf("full walk failed")
	}
	more, still, err := Run(g, done, 100)
	if err != nil || len(more) != 0 || !still.Done {
		t.Fatalf("resume of done must be empty, got %v", more)
	}
}

func starGraph(t *testing.T, leaves int) *graph.Graph {
	b := graph.NewBuilder()
	for i := 0; i < leaves; i++ {
		b.AddEdge("c", fmt.Sprintf("l%05d", i))
	}
	return b.Build()
}

func TestStarPeak(t *testing.T) {
	g := starGraph(t, 10000)
	for _, budget := range []int{10, 100, 1000} {
		_, fin, err := Run(g, Initial("c"), budget)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("star budget=%d visited=%d peak=%d bound=%d", budget, fin.CountVisited(), fin.CountPeakQueue(), 4*budget)
		if fin.CountVisited() != budget {
			t.Fatalf("visited %d != %d", fin.CountVisited(), budget)
		}
		if fin.CountPeakQueue() > 4*budget {
			t.Fatalf("peak %d exceeds 4*budget %d", fin.CountPeakQueue(), 4*budget)
		}
	}
}

func TestEdgesExamined(t *testing.T) {
	g := buildGraph(t, [][2]string{
		{"s", "a"}, {"s", "a"}, {"s", "b"}, {"a", "s"}, {"b", "c"}, {"c", "c"},
	}, "u")
	_, fin, err := Run(g, Initial("s"), 3)
	if err != nil {
		t.Fatal(err)
	}
	bound := 0
	for _, id := range fullOrder(g, "s") {
		bound += g.OutDegree(id)
	}
	full, done, err := Run(g, Initial("s"), 100)
	if err != nil {
		t.Fatal(err)
	}
	_ = full
	sum := 0
	for _, id := range []string{"s", "a", "b", "c"} {
		sum += g.OutDegree(id)
	}
	if done.CountEdgesExamined() > sum {
		t.Fatalf("edges %d > outdegree sum %d", done.CountEdgesExamined(), sum)
	}
	if fin.CountEdgesExamined() > bound {
		t.Fatalf("partial edges %d > bound %d", fin.CountEdgesExamined(), bound)
	}
}

func TestBoundaries(t *testing.T) {
	cases := []struct {
		name     string
		edges    [][2]string
		isolated []string
		start    string
		missing  bool
	}{
		{"empty", nil, nil, "x", true},
		{"start missing", [][2]string{{"a", "b"}}, nil, "x", true},
		{"self loop", [][2]string{{"a", "a"}}, nil, "a", false},
		{"cycle", [][2]string{{"a", "b"}, {"b", "a"}}, nil, "a", false},
		{"isolated node as start", [][2]string{{"x", "y"}}, nil, "z", true},
		{"zero outdegree node", nil, []string{"a"}, "a", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := buildGraph(t, tc.edges, tc.isolated...)
			out, fin, err := Run(g, Initial(tc.start), 50)
			if tc.missing {
				if !errors.Is(err, ErrMissingNode) {
					t.Fatalf("want missing error, got %v", err)
				}
				if id, ok := MissingNode(err); !ok || id != tc.start {
					t.Fatalf("missing node = %q", id)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !fin.Done || len(out) == 0 {
				t.Fatalf("walk did not finish: %v", out)
			}
			seen := map[string]bool{}
			for _, id := range out {
				if seen[id] {
					t.Fatalf("duplicate visit %q", id)
				}
				seen[id] = true
			}
		})
	}
}

func TestConcurrentResume(t *testing.T) {
	g := buildGraph(t, [][2]string{
		{"s", "b"}, {"s", "a"}, {"a", "c"}, {"b", "c"}, {"c", "s"},
	})
	_, st, err := Run(g, Initial("s"), 2)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make([][]string, 16)
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			out, fin, err := Run(g, st, 3)
			if err != nil {
				t.Error(err)
				return
			}
			results[i] = out
			if fin.CountVisited() != st.CountVisited()+len(out) {
				t.Error("counter leaked across goroutines")
			}
		}(i)
	}
	wg.Wait()
	for i := 1; i < len(results); i++ {
		if !reflect.DeepEqual(results[i], results[0]) {
			t.Fatalf("goroutine %d: %v != %v", i, results[i], results[0])
		}
	}
	if st.CountVisited() != 2 {
		t.Fatalf("source state mutated: %d", st.CountVisited())
	}
}
