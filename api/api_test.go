package api_test

import (
	"cmp"
	"errors"
	"fmt"
	"math/rand"
	"slices"
	"strings"
	"sync"
	"testing"

	"ontology/api"
)

var cycleEdges = [][2]string{{"a", "b"}, {"b", "c"}, {"c", "d"}, {"b", "d"}, {"d", "e"}, {"e", "b"}}

func chain(m int) [][2]string {
	e := make([][2]string, m)
	for i := 1; i <= m; i++ {
		e[i-1] = [2]string{fmt.Sprintf("a%d", i), fmt.Sprintf("a%d", i+1)}
	}
	return e
}

// naive is the test-side BFS reachability closure (paths of length >= 1),
// sorted; a node on a cycle reaches itself.
func naive(edges [][2]string) [][2]string {
	adj, nodes := map[string][]string{}, map[string]bool{}
	for _, e := range edges {
		adj[e[0]] = append(adj[e[0]], e[1])
		nodes[e[0]], nodes[e[1]] = true, true
	}
	var out [][2]string
	for s := range nodes {
		seen := map[string]bool{}
		q := append([]string(nil), adj[s]...)
		for i := 0; i < len(q); i++ {
			if u := q[i]; !seen[u] {
				seen[u] = true
				out = append(out, [2]string{s, u})
				q = append(q, adj[u]...)
			}
		}
	}
	slices.SortFunc(out, func(x, y [2]string) int {
		return cmp.Or(strings.Compare(x[0], y[0]), strings.Compare(x[1], y[1]))
	})
	return out
}

func mustDB(t *testing.T, edges [][2]string) *api.DB {
	t.Helper()
	db, err := api.New(edges)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return db
}

func TestSelfCheck(t *testing.T) {
	if err := api.SelfCheck(); err != nil {
		t.Fatal(err)
	}
}

func TestMatchesNaiveClosure(t *testing.T) {
	cases := map[string][][2]string{
		"cycle6": cycleEdges, "chain50": chain(50),
		"star": {{"s", "a"}, {"s", "b"}, {"a", "b"}}, "empty": {},
	}
	for name, edges := range cases {
		if got := mustDB(t, edges).Eval(); !slices.Equal(got, naive(edges)) {
			t.Errorf("%s: mismatch with naive closure", name)
		}
	}
	rng, base := rand.New(rand.NewSource(1)), chain(30)
	want := mustDB(t, base).Eval()
	for i := 0; i < 5; i++ { // random arrival order, same closure
		sh := slices.Clone(base)
		rng.Shuffle(len(sh), func(a, b int) { sh[a], sh[b] = sh[b], sh[a] })
		if got := mustDB(t, sh).Eval(); !slices.Equal(got, want) {
			t.Errorf("shuffle %d: closure differs", i)
		}
	}
}

func TestRejectLeavesStateUnchanged(t *testing.T) {
	if api.ErrEmptyNode == api.ErrDuplicateEdge ||
		api.ErrDuplicateEdge == api.ErrSelfLoop ||
		api.ErrSelfLoop == api.ErrEmptyNode {
		t.Fatal("sentinel errors not mutually distinct")
	}
	bads := [][2]string{{"", "x"}, {"x", ""}, {"a", "a"}, {"a", "b"}}
	errs := []error{api.ErrEmptyNode, api.ErrEmptyNode, api.ErrSelfLoop, api.ErrDuplicateEdge}
	db := mustDB(t, cycleEdges)
	before := db.Eval()
	for i, b := range bads {
		if err := db.AddEdge(b[0], b[1]); !errors.Is(err, errs[i]) {
			t.Errorf("AddEdge%v: err=%v, want %v", b, err, errs[i])
		}
		if got := db.Eval(); !slices.Equal(got, before) {
			t.Fatalf("AddEdge%v changed state", b)
		}
	}
	if _, err := api.New(append(slices.Clone(cycleEdges), [2]string{"z", "z"})); !errors.Is(err, api.ErrSelfLoop) {
		t.Errorf("New with bad edge: %v", err)
	}
	if err := db.AddEdge("e", "f"); err != nil { // still usable afterwards
		t.Fatal(err)
	}
	if got, want := db.Eval(), naive(append(slices.Clone(cycleEdges), [2]string{"e", "f"})); !slices.Equal(got, want) {
		t.Error("post-rejection AddEdge result wrong")
	}
}

func TestConcurrentEvalDeterministic(t *testing.T) {
	edges := append(slices.Clone(cycleEdges), chain(40)...)
	const n = 8
	results := make([][][2]string, n)
	shared := mustDB(t, edges)
	want := shared.Eval()
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = mustDB(t, edges).Eval() // independent evaluation
			if !slices.Equal(shared.Eval(), want) || api.SelfCheck() != nil {
				t.Errorf("goroutine %d: concurrent read failed", i)
			}
		}()
	}
	wg.Wait()
	for i := 1; i < n; i++ {
		if !slices.Equal(results[i], results[0]) {
			t.Fatalf("goroutine %d result differs", i)
		}
	}
}
