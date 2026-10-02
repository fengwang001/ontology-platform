package ontology

import (
	"fmt"
	"math/rand"
	"testing"
)

type refEdge struct {
	id     int
	from   int
	to     int
	weight int
	alive  bool
}

type reference struct {
	n      int
	source int
	max    int
	nextID int
	live   int
	edges  map[int]*refEdge
	dist   []int64
	parent []int
}

func newReference(n, source, max int) *reference {
	dist := make([]int64, n)
	for i := range dist {
		dist[i] = unreachable
	}
	dist[source] = 0
	parent := make([]int, n)
	for i := range parent {
		parent[i] = noParent
	}
	return &reference{
		n:      n,
		source: source,
		max:    max,
		nextID: 1,
		edges:  make(map[int]*refEdge),
		dist:   dist,
		parent: parent,
	}
}

func (r *reference) add(u, v, weight int) (int, bool) {
	if u < 0 || u >= r.n || v < 0 || v >= r.n || u == v || weight < 1 || weight > 1_000_000 || r.live == r.max {
		return 0, false
	}
	id := r.nextID
	r.edges[id] = &refEdge{id: id, from: u, to: v, weight: weight, alive: true}
	r.nextID++
	r.live++
	r.recompute()
	return id, true
}

func (r *reference) setWeight(id, weight int) bool {
	e, ok := r.edges[id]
	if weight < 1 || weight > 1_000_000 || !ok || !e.alive {
		return false
	}
	e.weight = weight
	r.recompute()
	return true
}

func (r *reference) remove(id int) bool {
	e, ok := r.edges[id]
	if !ok || !e.alive {
		return false
	}
	e.alive = false
	r.live--
	r.recompute()
	return true
}

func (r *reference) recompute() {
	dist := make([]int64, r.n)
	parent := make([]int, r.n)
	for i := 0; i < r.n; i++ {
		dist[i] = unreachable
		parent[i] = noParent
	}
	dist[r.source] = 0

	used := make([]bool, r.n)
	for iteration := 0; iteration < r.n; iteration++ {
		u := -1
		for v := 0; v < r.n; v++ {
			if !used[v] && dist[v] != unreachable && (u == -1 || dist[v] < dist[u]) {
				u = v
			}
		}
		if u == -1 {
			break
		}
		used[u] = true
		for _, e := range r.edges {
			if !e.alive || e.from != u {
				continue
			}
			candidate := dist[u] + int64(e.weight)
			if dist[e.to] == unreachable || candidate < dist[e.to] {
				dist[e.to] = candidate
			}
		}
	}

	for v := 0; v < r.n; v++ {
		if v == r.source || dist[v] == unreachable {
			continue
		}
		for _, e := range r.edges {
			if !e.alive || e.to != v || dist[e.from] == unreachable {
				continue
			}
			if dist[e.from]+int64(e.weight) == dist[v] && (parent[v] == noParent || e.id < parent[v]) {
				parent[v] = e.id
			}
		}
	}
	r.dist = dist
	r.parent = parent
}

func assertState(t *testing.T, s *Service, r *reference, op string, result UpdateResult) {
	t.Helper()
	for v := 0; v < s.n; v++ {
		distance, reachable, err := s.Dist(v)
		if err != nil {
			t.Fatalf("%s Dist(%d): %v", op, v, err)
		}
		if (r.dist[v] != unreachable) != reachable || (reachable && distance != r.dist[v]) {
			t.Fatalf("%s Dist(%d)=(%d,%v), reference=(%d,%v)", op, v, distance, reachable, r.dist[v], r.dist[v] != unreachable)
		}

		parent, hasParent, err := s.Parent(v)
		if err != nil {
			t.Fatalf("%s Parent(%d): %v", op, v, err)
		}
		if (r.parent[v] != noParent) != hasParent || (hasParent && parent != r.parent[v]) {
			t.Fatalf("%s Parent(%d)=(%d,%v), reference=%d", op, v, parent, hasParent, r.parent[v])
		}

		path, reachableByPath, err := s.Path(v)
		if err != nil || reachableByPath != reachable {
			t.Fatalf("%s Path(%d): %v reachable=%v/%v", op, v, err, reachableByPath, reachable)
		}
		if reachable {
			current := s.source
			sum := int64(0)
			for _, id := range path {
				e := s.edges[id]
				if e.from != current {
					t.Fatalf("%s Path(%d) edge %d is not contiguous", op, v, id)
				}
				current = e.to
				sum += int64(e.weight)
			}
			if current != v || sum != distance {
				t.Fatalf("%s Path(%d) ends at %d with length %d, expected %d", op, v, current, sum, distance)
			}
		}
	}

	expectedD, expectedP := changes(t, s, r, result.Version)
	if !sameInts(result.DChanged, expectedD) || !sameInts(result.PChanged, expectedP) {
		t.Fatalf("%s changes D=%v/P=%v, expected D=%v/P=%v", op, result.DChanged, result.PChanged, expectedD, expectedP)
	}
}

func changes(t *testing.T, s *Service, r *reference, version int) ([]int, []int) {
	dChanged := make([]int, 0)
	pChanged := make([]int, 0)
	if version == 0 {
		return dChanged, pChanged
	}
	previous := version - 1
	for v := 0; v < s.n; v++ {
		oldDistance, oldReachable, _ := s.DistAt(v, previous)
		oldParent, oldHasParent, _ := s.ParentAt(v, previous)
		if (oldReachable != (r.dist[v] != unreachable)) || (oldReachable && oldDistance != r.dist[v]) {
			dChanged = append(dChanged, v)
			continue
		}
		if oldReachable {
			if oldHasParent != (r.parent[v] != noParent) || (oldHasParent && oldParent != r.parent[v]) {
				pChanged = append(pChanged, v)
			}
		}
	}
	return dChanged, pChanged
}

func (s *Service) ParentAt(v, version int) (int, bool, error) {
	if !s.validateNode(v) {
		return 0, false, errorWith(InvalidArgument, "node is out of range")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if version > s.version {
		return 0, false, errorWith(VersionFuture, "version has not been produced")
	}
	oldest := s.history[0].version
	if version < oldest {
		return 0, false, errorWith(HistoryExpired, "version is no longer retained")
	}
	snap := s.history[version-oldest]
	if snap.parent[v] == noParent {
		return 0, false, nil
	}
	return snap.parent[v], true, nil
}

func sameInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestRandomComparison(t *testing.T) {
	const iterations = 2000
	rng := rand.New(rand.NewSource(1190))
	n := 20
	source := rng.Intn(n)
	maxEdges := 60
	keep := 8

	service, err := New(n, source, maxEdges, keep)
	if err != nil {
		t.Fatal(err)
	}
	model := newReference(n, source, maxEdges)

	for i := 0; i < iterations; i++ {
		choice := rng.Intn(10)
		switch {
		case choice < 5:
			u := rng.Intn(n)
			v := rng.Intn(n)
			weight := 1 + rng.Intn(10)
			result, addErr := service.AddEdge(u, v, weight)
			id, accepted := model.add(u, v, weight)
			if accepted != (addErr == nil) {
				t.Fatalf("iter %d AddEdge(%d,%d,%d) err=%v accepted=%v", i, u, v, weight, addErr, accepted)
			}
			t.Logf("input iter=%d AddEdge u=%d v=%d w=%d output version=%d id=%d D=%v P=%d accepted=%v basis=%s", i, u, v, weight, result.Version, result.EdgeID, result.DChanged, result.PChanged, accepted, "reference recomputation")
			if accepted {
				if result.EdgeID != id {
					t.Fatalf("AddEdge id=%d, reference=%d", result.EdgeID, id)
				}
				changed := append(append([]int{}, result.DChanged...), result.PChanged...)
				if limit := examinedLimit(service, changed); service.Examined() > limit {
					t.Fatalf("iter %d AddEdge examined=%d limit=%d", i, service.Examined(), limit)
				}
				assertState(t, service, model, fmt.Sprintf("iter %d AddEdge", i), result)
			}
		case choice < 8:
			id := 1 + rng.Intn(max(model.nextID, 1))
			weight := 1 + rng.Intn(10)
			result, setErr := service.SetWeight(id, weight)
			accepted := model.setWeight(id, weight)
			if accepted != (setErr == nil) {
				t.Fatalf("iter %d SetWeight(%d,%d) err=%v accepted=%v", i, id, weight, setErr, accepted)
			}
			t.Logf("input iter=%d SetWeight id=%d w=%d output version=%d D=%v P=%v accepted=%v basis=%s", i, id, weight, result.Version, result.DChanged, result.PChanged, accepted, "reference recomputation")
			if accepted {
				changed := append(append([]int{}, result.DChanged...), result.PChanged...)
				if limit := examinedLimit(service, changed); service.Examined() > limit {
					t.Fatalf("iter %d SetWeight examined=%d limit=%d", i, service.Examined(), limit)
				}
				assertState(t, service, model, fmt.Sprintf("iter %d SetWeight", i), result)
			}
		default:
			id := 1 + rng.Intn(max(model.nextID, 1))
			result, removeErr := service.RemoveEdge(id)
			accepted := model.remove(id)
			if accepted != (removeErr == nil) {
				t.Fatalf("iter %d RemoveEdge(%d) err=%v accepted=%v", i, id, removeErr, accepted)
			}
			t.Logf("input iter=%d RemoveEdge id=%d output version=%d D=%v P=%v accepted=%v basis=%s", i, id, result.Version, result.DChanged, result.PChanged, accepted, "reference recomputation")
			if accepted {
				changed := append(append([]int{}, result.DChanged...), result.PChanged...)
				if limit := examinedLimit(service, changed); service.Examined() > limit {
					t.Fatalf("iter %d RemoveEdge examined=%d limit=%d", i, service.Examined(), limit)
				}
				assertState(t, service, model, fmt.Sprintf("iter %d RemoveEdge", i), result)
			}
		}
	}
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
