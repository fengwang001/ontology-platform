package ontology

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"sync"
	"testing"
)

// naiveModel is an independent, deliberately simple reference model: it
// rebuilds the effective subgraph from raw graph state applying the mandated
// filter order (existence, then traversal) and enumerates every directed
// simple cycle by depth-first path extension.
type naiveModel struct {
	adj     map[string][]string // to-ids only (parallel/dup edges collapse)
	objects map[string]bool
}

func buildNaive(g *Graph, caller string) naiveModel {
	g.mu.RLock()
	defer g.mu.RUnlock()
	v := g.callers[caller]
	m := naiveModel{
		adj:     map[string][]string{},
		objects: map[string]bool{},
	}
	if v == nil {
		return m
	}
	// Filter 1: existence.
	visible := map[string]bool{}
	for id := range v.existenceGranted {
		if _, ok := g.objects[id]; ok {
			visible[id] = true
			m.objects[id] = true
		}
	}
	// Filter 2: traversal, on links surviving filter 1.
	for lid := range v.traversalGranted {
		l := g.links[lid]
		if l == nil || !visible[l.sourceID] || !visible[l.targetID] {
			continue
		}
		m.adj[l.sourceID] = append(m.adj[l.sourceID], l.targetID)
		if l.direction == Bidirectional {
			m.adj[l.targetID] = append(m.adj[l.targetID], l.sourceID)
		}
	}
	for u := range m.adj {
		sort.Strings(m.adj[u])
	}
	return m
}

// hasCycle is the naive boolean predicate (DFS three-color).
func (m naiveModel) hasCycle() bool {
	color := map[string]int{} // 0 white, 1 gray, 2 black
	var dfs func(u string) bool
	dfs = func(u string) bool {
		color[u] = 1
		for _, w := range m.adj[u] {
			switch color[w] {
			case 1:
				return true
			case 0:
				if dfs(w) {
					return true
				}
			}
		}
		color[u] = 2
		return false
	}
	nodes := append([]string(nil), mapsKeys(m.objects)...)
	sort.Strings(nodes)
	for _, u := range nodes {
		if color[u] == 0 && dfs(u) {
			return true
		}
	}
	return false
}

// cycles enumerates every distinct directed simple cycle as the vertex set
// {s, ..., s}, canonicalized to start at its minimum vertex. Only suitable
// for small random graphs.
func (m naiveModel) cycles() [][]string {
	var found [][]string
	seen := map[string]bool{}
	onPath := map[string]bool{}
	var dfs func(start, u string, path []string)
	dfs = func(start, u string, path []string) {
		for _, w := range m.adj[u] {
			if w == start {
				cycle := append(append([]string{}, path...), start)
				key := fmt.Sprint(cycle)
				if !seen[key] {
					seen[key] = true
					found = append(found, cycle)
				}
				continue
			}
			if onPath[w] || w < start {
				continue // only enumerate each rotation once
			}
			onPath[w] = true
			dfs(start, w, append(path, w))
			onPath[w] = false
		}
	}
	nodes := append([]string(nil), mapsKeys(m.objects)...)
	sort.Strings(nodes)
	for _, s := range nodes {
		onPath[s] = true
		dfs(s, s, []string{s})
		onPath[s] = false
	}
	return found
}

func mapsKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// validCycle verifies evidence independently against the naive model:
// distinct internal vertices, closing repetition at the end, each consecutive
// pair adjacent.
func validCycle(m naiveModel, ev []string) bool {
	if len(ev) < 2 || ev[0] != ev[len(ev)-1] {
		return false
	}
	internal := ev[:len(ev)-1]
	uniq := map[string]bool{}
	for _, v := range internal {
		if !m.objects[v] || uniq[v] {
			return false
		}
		uniq[v] = true
	}
	for i := 0; i+1 < len(ev); i++ {
		if !contains(m.adj[ev[i]], ev[i+1]) {
			return false
		}
	}
	return true
}

func contains(s []string, x string) bool {
	for _, v := range s {
		if v == x {
			return true
		}
	}
	return false
}

// TestRandomDifferential runs many randomly generated ontology states and
// compares HasCycle against the naive model, recording every call.
func TestRandomDifferential(t *testing.T) {
	const iterations = 300
	type recorded struct {
		caller   string
		result   CycleResult
		naive    bool
		evidence []string
	}
	var records []recorded

	for seed := int64(0); seed < iterations; seed++ {
		rng := rand.New(rand.NewSource(seed))
		g := mkGraph(t)
		nObj := 1 + rng.Intn(8)
		var ids []string
		for i := 0; i < nObj; i++ {
			id := fmt.Sprintf("o%d", i)
			must(t, g.AddObject("admin", id, "T"))
			ids = append(ids, id)
		}
		callers := []string{"alice", "bob", "carol"}
		// Random permissions: existence first conceptually; grants are
		// independent draws.
		for _, c := range callers {
			for _, id := range ids {
				if rng.Intn(2) == 0 {
					must(t, g.GrantExistence(c, id))
				}
			}
		}
		nLinks := rng.Intn(14)
		for i := 0; i < nLinks; i++ {
			kind := []string{"D", "B"}[rng.Intn(2)]
			s := ids[rng.Intn(len(ids))]
			tgt := ids[rng.Intn(len(ids))]
			lid := fmt.Sprintf("r%d", i)
			err := g.AddLink("admin", lid, kind, s, tgt)
			if err != nil {
				continue // self loops on disallowed types etc.
			}
			for _, c := range callers {
				if rng.Intn(2) == 0 {
					must(t, g.GrantTraversal(c, lid))
				}
			}
		}

		for _, c := range callers {
			// Determinism: repeated identical-state calls agree exactly.
			res1, err := g.HasCycle(c)
			must(t, err)
			res2, err := g.HasCycle(c)
			must(t, err)
			if res1.HasCycle != res2.HasCycle ||
				!reflect.DeepEqual(res1.Evidence, res2.Evidence) {
				t.Fatalf("seed=%d caller=%s nondeterministic: %v vs %v",
					seed, c, res1.Evidence, res2.Evidence)
			}

			m := buildNaive(g, c)
			if res1.HasCycle != m.hasCycle() {
				t.Fatalf("seed=%d caller=%s mismatch: impl=%v naive=%v",
					seed, c, res1.HasCycle, m.hasCycle())
			}
			if res1.HasCycle {
				if !validCycle(m, res1.Evidence) {
					t.Fatalf("seed=%d caller=%s invalid evidence %v",
						seed, c, res1.Evidence)
				}
				// Evidence must be one of the cycles the naive enumerator
				// finds, and every evidence vertex must belong to that cycle.
				wantSet := cycleSet(res1.Evidence)
				matched := false
				for _, cy := range m.cycles() {
					if reflect.DeepEqual(cycleSet(cy), wantSet) {
						matched = true
						break
					}
				}
				if !matched {
					t.Fatalf("seed=%d caller=%s evidence %v not a naive cycle",
						seed, c, res1.Evidence)
				}
			}
			evCopy := append([]string(nil), res1.Evidence...)
			records = append(records, recorded{
				caller: c, result: res1, naive: m.hasCycle(), evidence: evCopy,
			})
		}
	}
	if len(records) != iterations*3 {
		t.Fatalf("recorded %d calls", len(records))
	}
	// Sanity: both outcomes should have been observed over the run.
	cycles, acyclic := 0, 0
	for _, r := range records {
		if r.naive {
			cycles++
		} else {
			acyclic++
		}
	}
	if cycles == 0 || acyclic == 0 {
		t.Fatalf("poor random coverage: cycles=%d acyclic=%d", cycles, acyclic)
	}
	t.Logf("recorded %d HasCycle calls (%d cyclic, %d acyclic)",
		len(records), cycles, acyclic)
}

func cycleSet(ev []string) []string {
	set := map[string]bool{}
	for _, v := range ev {
		set[v] = true
	}
	out := mapsKeys(set)
	sort.Strings(out)
	return out
}

// TestConcurrentSerializable runs concurrent detections and mutations. The
// race detector checks internal synchronization; barrier phases require all
// concurrent detections to agree exactly.
func TestConcurrentSerializable(t *testing.T) {
	g := mkGraph(t)
	addObjs(t, g, "admin", "a", "b", "c")
	must(t, g.AddLink("admin", "ab", "D", "a", "b"))
	must(t, g.AddLink("admin", "bc", "D", "b", "c"))
	must(t, g.AddLink("admin", "ca", "D", "c", "a"))
	must(t, g.GrantExistence("zoe", "a", "b", "c"))

	stop := make(chan struct{})
	var wg sync.WaitGroup

	// Mutator flips traversal on the closing edge.
	wg.Add(1)
	go func() {
		defer wg.Done()
		granted := false
		for {
			select {
			case <-stop:
				return
			default:
			}
			if granted {
				_ = g.RevokeTraversal("zoe", "ca")
			} else {
				_ = g.GrantTraversal("zoe", "ca")
			}
			granted = !granted
		}
	}()

	// Readers must only observe consistent states: cyclic answers come with a
	// valid three-object cycle, acyclic answers with empty evidence.
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				res, err := g.HasCycle("zoe")
				if err != nil {
					t.Errorf("unexpected error: %v", err)
					return
				}
				if res.HasCycle {
					if !reflect.DeepEqual(res.Evidence, []string{"a", "b", "c", "a"}) {
						t.Errorf("torn read, evidence=%v", res.Evidence)
						return
					}
				} else if len(res.Evidence) != 0 {
					t.Errorf("acyclic result carried evidence: %v", res.Evidence)
					return
				}
			}
		}()
	}

	// Barrier phase: with mutations stopped, all concurrent detections must
	// be identical.
	close(stop)
	wg.Wait()

	const readers = 8
	var reswg sync.WaitGroup
	results := make([]CycleResult, readers)
	for i := 0; i < readers; i++ {
		reswg.Add(1)
		go func(i int) {
			defer reswg.Done()
			r, err := g.HasCycle("zoe")
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			results[i] = r
		}(i)
	}
	reswg.Wait()
	for i := 1; i < readers; i++ {
		if !reflect.DeepEqual(results[i], results[0]) {
			t.Fatalf("barrier mismatch: %+v vs %+v", results[i], results[0])
		}
	}
}
