package ontology

import (
	"errors"
	"math/rand"
	"reflect"
	"sort"
	"sync"
	"testing"
)

type testEdge struct {
	id     int
	u, v   int
	weight int64
	alive  bool
}

func naiveMSF(edges map[int]*testEdge, n int) (map[int]bool, int64, int) {
	parent := make([]int, n)
	for i := range parent {
		parent[i] = i
	}
	var find func(int) int
	find = func(x int) int {
		if parent[x] != x {
			parent[x] = find(parent[x])
		}
		return parent[x]
	}
	ids := make([]int, 0)
	for id, edge := range edges {
		if edge.alive {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool {
		a, b := edges[ids[i]], edges[ids[j]]
		return edgeLess(a.weight, a.id, b.weight, b.id)
	})
	tree := make(map[int]bool)
	var total int64
	components := n
	for _, id := range ids {
		edge := edges[id]
		a, b := find(edge.u), find(edge.v)
		if a != b {
			parent[a] = b
			tree[id] = true
			total += edge.weight
			components--
		}
	}
	return tree, total, components
}

func mustSet(t *testing.T, m *DynamicMSF, id int, weight int64) UpdateResult {
	t.Helper()
	result, err := m.SetWeight(id, weight)
	if err != nil {
		t.Fatalf("SetWeight(%d,%d): %v", id, weight, err)
	}
	return result
}

func TestExampleSequence(t *testing.T) {
	m, err := NewDynamicMSF(5, 20)
	if err != nil {
		t.Fatal(err)
	}
	id, result, err := m.AddEdge(0, 1, 4)
	if err != nil || id != 1 || !reflect.DeepEqual(result.Entered, []int{1}) || result.Weight != 4 || result.Components != 4 {
		t.Fatalf("add 1: id=%d result=%+v err=%v", id, result, err)
	}
	id, result, err = m.AddEdge(1, 2, 6)
	if err != nil || id != 2 || !reflect.DeepEqual(result.Entered, []int{2}) || result.Weight != 10 {
		t.Fatalf("add 2: id=%d result=%+v err=%v", id, result, err)
	}
	id, result, err = m.AddEdge(0, 2, 6)
	if err != nil || id != 3 || len(result.Entered) != 0 || len(result.Left) != 0 || result.Weight != 10 {
		t.Fatalf("add 3: id=%d result=%+v err=%v", id, result, err)
	}
	id, result, err = m.AddEdge(0, 2, 5)
	if err != nil || id != 4 || !reflect.DeepEqual(result.Entered, []int{4}) || !reflect.DeepEqual(result.Left, []int{2}) || result.Weight != 9 {
		t.Fatalf("add 4: id=%d result=%+v err=%v", id, result, err)
	}
	if result = mustSet(t, m, 3, 5); !reflect.DeepEqual(result.Entered, []int{3}) || !reflect.DeepEqual(result.Left, []int{4}) || result.Version != 5 || result.Weight != 9 {
		t.Fatalf("set 3: %+v", result)
	}
	if since, _ := m.TreeSince(3); since != 5 {
		t.Fatalf("TreeSince(3)=%d", since)
	}
	if result = mustSet(t, m, 1, 10); !reflect.DeepEqual(result.Entered, []int{2}) || !reflect.DeepEqual(result.Left, []int{1}) || result.Weight != 11 {
		t.Fatalf("set 1: %+v", result)
	}
	if result, err = m.RemoveEdge(2); err != nil || !reflect.DeepEqual(result.Entered, []int{1}) || !reflect.DeepEqual(result.Left, []int{2}) || result.Weight != 15 {
		t.Fatalf("remove 2: %+v err=%v", result, err)
	}
	if result, err = m.RemoveEdge(1); err != nil || len(result.Entered) != 0 || !reflect.DeepEqual(result.Left, []int{1}) || result.Weight != 5 || result.Components != 4 {
		t.Fatalf("remove 1: %+v err=%v", result, err)
	}
	if result = mustSet(t, m, 3, 2); len(result.Entered) != 0 || len(result.Left) != 0 || result.Version != 9 {
		t.Fatalf("decrease forest edge: %+v", result)
	}
	if since, _ := m.TreeSince(3); since != 5 {
		t.Fatalf("TreeSince after decrease=%d", since)
	}
	if result = mustSet(t, m, 3, 100); !reflect.DeepEqual(result.Entered, []int{4}) || !reflect.DeepEqual(result.Left, []int{3}) || result.Version != 10 {
		t.Fatalf("increase forest edge: %+v", result)
	}
	if since, _ := m.TreeSince(4); since != 10 {
		t.Fatalf("TreeSince(4)=%d", since)
	}
	if since, _ := m.TreeSince(3); since != 0 {
		t.Fatalf("TreeSince(3)=%d", since)
	}
}

func TestTieWeightPathMaxAndQueries(t *testing.T) {
	m, _ := NewDynamicMSF(4, 10)
	add := func(u, v int, w int64) int {
		t.Helper()
		id, _, err := m.AddEdge(u, v, w)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	add(0, 1, 5)
	add(1, 2, 5)
	c := add(2, 3, 5)
	pe, err := m.PathMax(0, 3)
	if err != nil || pe.ID != c || pe.Weight != 5 {
		t.Fatalf("PathMax=%+v err=%v", pe, err)
	}
	if ok, err := m.Connected(0, 3); err != nil || !ok {
		t.Fatalf("Connected=%v err=%v", ok, err)
	}
	if _, err = m.PathMax(1, 1); !errors.Is(err, ErrSameNode) {
		t.Fatalf("same node err=%v", err)
	}
	if _, err = m.PathMax(0, 4); !errors.Is(err, ErrInvalidArguments) {
		t.Fatalf("invalid node err=%v", err)
	}
	if pe, err = m.PathMax(0, 3); err != nil || pe.ID != c {
		t.Fatalf("connected PathMax=%+v err=%v", pe, err)
	}
}

func TestPathMaxNotConnectedAndConstructionErrors(t *testing.T) {
	if _, err := NewDynamicMSF(0, 1); !errors.Is(err, ErrInvalidArguments) {
		t.Fatalf("n=0 err=%v", err)
	}
	if _, err := NewDynamicMSF(100001, 1); !errors.Is(err, ErrInvalidArguments) {
		t.Fatalf("n too large err=%v", err)
	}
	if _, err := NewDynamicMSF(2, 0); !errors.Is(err, ErrInvalidArguments) {
		t.Fatalf("E=0 err=%v", err)
	}
	if _, err := NewDynamicMSF(2, 500001); !errors.Is(err, ErrInvalidArguments) {
		t.Fatalf("E too large err=%v", err)
	}
	m, _ := NewDynamicMSF(3, 5)
	m.AddEdge(0, 1, 1)
	if _, err := m.PathMax(0, 2); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("PathMax disconnected err=%v", err)
	}
	if ok, err := m.Connected(0, 2); err != nil || ok {
		t.Fatalf("Connected disconnected=%v err=%v", ok, err)
	}
}

func TestRejectionsDoNotConsumeIDOrVersion(t *testing.T) {
	m, _ := NewDynamicMSF(2, 1)
	if _, _, err := m.AddEdge(0, 0, 1); !errors.Is(err, ErrInvalidArguments) {
		t.Fatal(err)
	}
	id, first, err := m.AddEdge(0, 1, 1)
	if err != nil || id != 1 || first.Version != 1 {
		t.Fatalf("accepted id=%d result=%+v err=%v", id, first, err)
	}
	if _, _, err = m.AddEdge(1, 0, 2); !errors.Is(err, ErrEdgeLimit) {
		t.Fatal(err)
	}
	if _, err = m.SetWeight(9, 3); !errors.Is(err, ErrEdgeNotFound) {
		t.Fatal(err)
	}
	if _, err = m.SetWeight(1, 1_000_000_001); !errors.Is(err, ErrInvalidArguments) {
		t.Fatal(err)
	}
	if result, err := m.SetWeight(1, 1); err != nil || result.Version != 2 {
		t.Fatalf("same weight version result=%+v err=%v", result, err)
	}
	if _, err = m.RemoveEdge(9); !errors.Is(err, ErrEdgeNotFound) {
		t.Fatal(err)
	}
	if _, err = m.InForest(9); !errors.Is(err, ErrEdgeNotFound) {
		t.Fatal(err)
	}
	if _, err = m.TreeSince(9); !errors.Is(err, ErrEdgeNotFound) {
		t.Fatal(err)
	}
}

func TestScannedLongChainLeafSide(t *testing.T) {
	const n = 100000
	m, err := NewDynamicMSF(n, n+1)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i+1 < n; i++ {
		if _, _, err := m.AddEdge(i, i+1, 1); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := m.AddEdge(0, 1, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := m.RemoveEdge(1); err != nil {
		t.Fatal(err)
	}
	if m.scanned > 4 {
		t.Fatalf("scanned=%d, want <= 4", m.scanned)
	}
}

func activeIDs(edges map[int]*testEdge) []int {
	ids := make([]int, 0, len(edges))
	for id, edge := range edges {
		if edge.alive {
			ids = append(ids, id)
		}
	}
	sort.Ints(ids)
	return ids
}

func anyAssignedID(rng *rand.Rand, edges map[int]*testEdge) int {
	ids := make([]int, 0, len(edges))
	for id := range edges {
		ids = append(ids, id)
	}
	return ids[rng.Intn(len(ids))]
}

func sortedSetDiff(a, b map[int]bool) []int {
	result := make([]int, 0)
	for id := range a {
		if !b[id] {
			result = append(result, id)
		}
	}
	sort.Ints(result)
	return result
}

func TestRandomAgainstKruskal(t *testing.T) {
	const sequences = 2000
	rng := rand.New(rand.NewSource(20261002))
	for sequence := 0; sequence < sequences; sequence++ {
		n := 2 + rng.Intn(7)
		maxEdges := 1 + rng.Intn(18)
		m, err := NewDynamicMSF(n, maxEdges)
		if err != nil {
			t.Fatal(err)
		}
		edges := make(map[int]*testEdge)
		nextID := 1
		version := 0
		since := make(map[int]int)
		before, beforeWeight, beforeComponents := naiveMSF(edges, n)

		stop := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(1)
		queryRng := rand.New(rand.NewSource(rng.Int63()))
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					u, v := queryRng.Intn(n), queryRng.Intn(n)
					_, _ = m.Connected(u, v)
					_, _ = m.PathMax(u, v)
					_ = m.Weight()
					_ = m.Components()
				}
			}
		}()

		for step := 0; step < 90; step++ {
			action := rng.Intn(10)
			if action < 5 {
				u, v := rng.Intn(n), rng.Intn(n)
				weight := int64(rng.Intn(11) - 5)
				if rng.Intn(20) == 0 {
					weight = 1_000_000_001
				}
				aliveCount := len(activeIDs(edges))
				id, result, addErr := m.AddEdge(u, v, weight)
				switch {
				case u == v || weight < -1_000_000_000 || weight > 1_000_000_000:
					if !errors.Is(addErr, ErrInvalidArguments) {
						t.Fatalf("seq=%d invalid add err=%v", sequence, addErr)
					}
				case aliveCount == maxEdges:
					if !errors.Is(addErr, ErrEdgeLimit) {
						t.Fatalf("seq=%d limit err=%v", sequence, addErr)
					}
				default:
					if addErr != nil {
						t.Fatalf("seq=%d add err=%v", sequence, addErr)
					}
					if id != nextID {
						t.Fatalf("seq=%d id=%d want %d", sequence, id, nextID)
					}
					edges[id] = &testEdge{id: id, u: u, v: v, weight: weight, alive: true}
					nextID++
					version++
					t.Logf("seq=%d step=%d input=AddEdge(%d,%d,%d) output=version:%d entered:%v left:%v weight:%d components:%d criterion=Kruskal-before/after-symmetric-difference", sequence, step, u, v, weight, result.Version, result.Entered, result.Left, result.Weight, result.Components)
					after, afterWeight, afterComponents := naiveMSF(edges, n)
					assertRandomUpdate(t, m, sequence, step, version, result, before, after, beforeWeight, afterWeight, beforeComponents, afterComponents, edges, since)
					before, beforeWeight, beforeComponents = after, afterWeight, afterComponents
				}
				continue
			}

			if len(edges) == 0 {
				continue
			}
			id := anyAssignedID(rng, edges)
			if action < 8 {
				weight := int64(rng.Intn(11) - 5)
				if rng.Intn(20) == 0 {
					weight = -1_000_000_001
				}
				result, setErr := m.SetWeight(id, weight)
				if weight < -1_000_000_000 || weight > 1_000_000_000 {
					if !errors.Is(setErr, ErrInvalidArguments) {
						t.Fatalf("seq=%d invalid set err=%v", sequence, setErr)
					}
					continue
				}
				if !edges[id].alive {
					if !errors.Is(setErr, ErrEdgeNotFound) {
						t.Fatalf("seq=%d deleted set err=%v", sequence, setErr)
					}
					continue
				}
				if setErr != nil {
					t.Fatalf("seq=%d set err=%v", sequence, setErr)
				}
				edges[id].weight = weight
				version++
				t.Logf("seq=%d step=%d input=SetWeight(%d,%d) output=version:%d entered:%v left:%v weight:%d components:%d criterion=Kruskal-before/after-symmetric-difference", sequence, step, id, weight, result.Version, result.Entered, result.Left, result.Weight, result.Components)
				after, afterWeight, afterComponents := naiveMSF(edges, n)
				assertRandomUpdate(t, m, sequence, step, version, result, before, after, beforeWeight, afterWeight, beforeComponents, afterComponents, edges, since)
				before, beforeWeight, beforeComponents = after, afterWeight, afterComponents
				continue
			}

			result, removeErr := m.RemoveEdge(id)
			if !edges[id].alive {
				if !errors.Is(removeErr, ErrEdgeNotFound) {
					t.Fatalf("seq=%d deleted remove err=%v", sequence, removeErr)
				}
				continue
			}
			if removeErr != nil {
				t.Fatalf("seq=%d remove err=%v", sequence, removeErr)
			}
			edges[id].alive = false
			version++
			t.Logf("seq=%d step=%d input=RemoveEdge(%d) output=version:%d entered:%v left:%v weight:%d components:%d criterion=Kruskal-before/after-symmetric-difference", sequence, step, id, result.Version, result.Entered, result.Left, result.Weight, result.Components)
			after, afterWeight, afterComponents := naiveMSF(edges, n)
			assertRandomUpdate(t, m, sequence, step, version, result, before, after, beforeWeight, afterWeight, beforeComponents, afterComponents, edges, since)
			before, beforeWeight, beforeComponents = after, afterWeight, afterComponents
		}
		close(stop)
		wg.Wait()
	}
}

func assertRandomUpdate(t *testing.T, m *DynamicMSF, sequence, step, expectedVersion int, result UpdateResult, before, after map[int]bool, beforeWeight, afterWeight int64, beforeComponents, afterComponents int, edges map[int]*testEdge, since map[int]int) {
	t.Helper()
	entered := sortedSetDiff(after, before)
	left := sortedSetDiff(before, after)
	if result.Version != expectedVersion {
		t.Fatalf("seq=%d step=%d version=%d want %d", sequence, step, result.Version, expectedVersion)
	}
	if !reflect.DeepEqual(result.Entered, entered) || !reflect.DeepEqual(result.Left, left) {
		t.Fatalf("seq=%d step=%d report entered=%v left=%v want entered=%v left=%v", sequence, step, result.Entered, result.Left, entered, left)
	}
	if result.Weight != afterWeight || m.Weight() != afterWeight {
		t.Fatalf("seq=%d step=%d weight result=%d live=%d want %d", sequence, step, result.Weight, m.Weight(), afterWeight)
	}
	if result.Components != afterComponents || m.Components() != afterComponents {
		t.Fatalf("seq=%d step=%d components result=%d live=%d want %d", sequence, step, result.Components, m.Components(), afterComponents)
	}
	for id, edge := range edges {
		if !edge.alive {
			continue
		}
		got, err := m.InForest(id)
		if err != nil {
			t.Fatalf("seq=%d step=%d InForest(%d): %v", sequence, step, id, err)
		}
		if got != after[id] {
			t.Fatalf("seq=%d step=%d InForest(%d)=%v want %v", sequence, step, id, got, after[id])
		}
		if after[id] && !before[id] {
			since[id] = expectedVersion
		}
		if !after[id] {
			since[id] = 0
		}
		gotSince, err := m.TreeSince(id)
		if err != nil || gotSince != since[id] {
			t.Fatalf("seq=%d step=%d TreeSince(%d)=%d,%v want %d", sequence, step, id, gotSince, err, since[id])
		}
	}
	if len(after) != m.n-afterComponents {
		t.Fatalf("seq=%d step=%d forest size=%d want %d", sequence, step, len(after), m.n-afterComponents)
	}
}
