package ontology

import (
	"errors"
	"math"
	"math/rand"
	"sync"
	"testing"
)

func TestLongChainClosureStepsBoundAndReport(t *testing.T) {
	const n = 100000
	m, err := NewIncrementalBridges(n, n)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i+1 < n; i++ {
		result, err := m.AddEdge(i, i+1, int64(1+i%1_000_000))
		if err != nil || result.Kind != KindLink {
			t.Fatalf("link %d: %+v %v", i, result, err)
		}
	}
	result, err := m.AddEdge(0, n-1, 999_999)
	if err != nil {
		t.Fatal(err)
	}
	if result.Kind != KindMerge || len(result.Unbridged) != n-1 || result.NewLabel != 0 {
		t.Fatalf("closure result=%+v", result)
	}
	if result.Unbridged[0] != 1 || result.Unbridged[n-2] != n-1 {
		t.Fatalf("unbridged order = %d...%d", result.Unbridged[0], result.Unbridged[n-2])
	}
	if m.BridgeCount() != 0 || m.BlockCount() != 1 || m.ComponentCount() != 1 {
		t.Fatalf("counts bridges=%d blocks=%d comps=%d", m.BridgeCount(), m.BlockCount(), m.ComponentCount())
	}
	allowed := int64(n)*(int64(math.Ceil(math.Log2(float64(n))))+2) + 4*int64(n)
	if m.Steps() > allowed {
		t.Fatalf("steps=%d allowed=%d", m.Steps(), allowed)
	}
}

func TestRandom500000StepsBound(t *testing.T) {
	const n = 100000
	const e = 500000
	m, err := NewIncrementalBridges(n, e)
	if err != nil {
		t.Fatal(err)
	}
	rng := rand.New(rand.NewSource(42))
	accepted := 0
	for i := 0; i < e; i++ {
		u, v := rng.Intn(n), rng.Intn(n)
		if u == v {
			v = (v + 1) % n
		}
		result, err := m.AddEdge(u, v, int64(1+rng.Intn(1_000_000)))
		if err != nil {
			t.Fatal(err)
		}
		accepted++
		if result.Kind == KindMerge {
			if got := m.BridgeCount() + m.ComponentCount(); got != m.BlockCount() {
				t.Fatalf("invariant after merge: bridges+comps=%d blocks=%d", got, m.BlockCount())
			}
		}
	}
	allowed := int64(n)*(int64(math.Ceil(math.Log2(float64(n))))+2) + 4*int64(accepted)
	if m.Steps() > allowed {
		t.Fatalf("steps=%d allowed=%d", m.Steps(), allowed)
	}
}

func TestUnrelatedBridgeSurvivesMergeAndFutureConnect(t *testing.T) {
	m, _ := NewIncrementalBridges(6, 20)
	addForTest(t, m, 0, 1, 1, KindLink)
	edge2 := addForTest(t, m, 1, 2, 1, KindLink).ID
	addForTest(t, m, 2, 3, 1, KindLink)
	addForTest(t, m, 4, 5, 1, KindLink)
	merge := addForTest(t, m, 0, 2, 2, KindMerge)
	if !equalInts(merge.Unbridged, []int{1, 2}) {
		t.Fatalf("merge=%+v", merge)
	}
	stillBridge, _ := m.IsBridge(edge2)
	if stillBridge {
		t.Fatalf("edge %d should be nonbridge", edge2)
	}
	link := addForTest(t, m, 3, 4, 7, KindLink)
	if !mustBridge(t, m, link.ID) {
		t.Fatalf("new link should be bridge: %+v", link)
	}
	path, err := m.BridgesOnPath(0, 5)
	if err != nil || !equalInts(path, []int{3, 4, link.ID}) {
		t.Fatalf("path=%v err=%v", path, err)
	}
	secondMerge := addForTest(t, m, 2, 5, 3, KindMerge)
	if !equalInts(secondMerge.Unbridged, []int{3, 4, link.ID}) {
		t.Fatalf("second merge=%+v", secondMerge)
	}
}

func TestValidationOrder(t *testing.T) {
	if _, err := NewIncrementalBridges(0, 1); !errors.Is(err, ErrInvalidN) {
		t.Fatalf("N error=%v", err)
	}
	if _, err := NewIncrementalBridges(1, 0); !errors.Is(err, ErrInvalidE) {
		t.Fatalf("E error=%v", err)
	}
	m, _ := NewIncrementalBridges(2, 1)
	if _, err := m.AddEdge(2, 0, 1); !errors.Is(err, ErrInvalidNode) {
		t.Fatalf("node before self/weight: %v", err)
	}
	if _, err := m.AddEdge(0, 0, 0); !errors.Is(err, ErrSelfLoop) {
		t.Fatalf("self before weight: %v", err)
	}
	m.AddEdge(0, 1, 1)
	if _, err := m.AddEdge(0, 1, 0); !errors.Is(err, ErrInvalidWeight) {
		t.Fatalf("invalid before limit: %v", err)
	}
	if _, err := m.AddEdge(0, 1, 2); !errors.Is(err, ErrEdgeLimit) {
		t.Fatalf("limit: %v", err)
	}
	if _, err := m.Block(2); !errors.Is(err, ErrInvalidNode) {
		t.Fatalf("block node: %v", err)
	}
	if _, err := m.BridgesOnPath(0, 2); !errors.Is(err, ErrInvalidNode) {
		t.Fatalf("path node: %v", err)
	}
	m2, _ := NewIncrementalBridges(2, 2)
	if _, err := m2.BridgesOnPath(0, 1); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("path disconnected: %v", err)
	}
	if _, err := m2.MinBridge(0, 1); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("min disconnected: %v", err)
	}
	m2.AddEdge(0, 1, 1)
	m2.AddEdge(0, 1, 2)
	if _, err := m2.MinBridge(0, 1); !errors.Is(err, ErrNoBridgeOnPath) {
		t.Fatalf("min no-bridge error=%v", err)
	}
}

func TestConcurrentQueriesAndAdds(t *testing.T) {
	m, _ := NewIncrementalBridges(64, 1000)
	var wg sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			for i := 0; i < 300; i++ {
				u, v := rng.Intn(64), rng.Intn(64)
				if u != v && rng.Intn(3) == 0 {
					_, _ = m.AddEdge(u, v, int64(1+rng.Intn(10)))
				} else {
					_, _ = m.Connected(u, v)
					_, _ = m.Block(u)
					_, _ = m.BridgesOnPath(u, v)
					_, _ = m.MinBridge(u, v)
				}
			}
		}(int64(worker + 1))
	}
	wg.Wait()
	if m.BridgeCount()+m.ComponentCount() != m.BlockCount() {
		t.Fatalf("concurrent invariant failed: %d+%d != %d", m.BridgeCount(), m.ComponentCount(), m.BlockCount())
	}
}

func equalInts(a, b []int) bool {
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
