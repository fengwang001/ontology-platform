package graph

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sync"
	"testing"
)

func mustAdd(t *testing.T, m *Maintainer, u, v, w int) AddResult {
	t.Helper()
	res, err := m.AddEdge(u, v, w)
	if err != nil {
		t.Fatalf("AddEdge(%d,%d,%d): unexpected error: %v", u, v, w, err)
	}
	return res
}

func mustBridges(t *testing.T, m *Maintainer, u, v int) []int {
	t.Helper()
	ids, err := m.BridgesOnPath(u, v)
	if err != nil {
		t.Fatalf("BridgesOnPath(%d,%d): unexpected error: %v", u, v, err)
	}
	return ids
}

func mustBlock(t *testing.T, m *Maintainer, x int) int {
	t.Helper()
	l, err := m.Block(x)
	if err != nil {
		t.Fatalf("Block(%d): unexpected error: %v", x, err)
	}
	return l
}

func mustWeight(t *testing.T, m *Maintainer, x int) int64 {
	t.Helper()
	w, err := m.BlockWeight(x)
	if err != nil {
		t.Fatalf("BlockWeight(%d): unexpected error: %v", x, err)
	}
	return w
}

func mustIsBridge(t *testing.T, m *Maintainer, id int) bool {
	t.Helper()
	b, err := m.IsBridge(id)
	if err != nil {
		t.Fatalf("IsBridge(%d): unexpected error: %v", id, err)
	}
	return b
}

func mustHistory(t *testing.T, m *Maintainer, id int) (int, int) {
	t.Helper()
	a, f, err := m.EdgeHistory(id)
	if err != nil {
		t.Fatalf("EdgeHistory(%d): unexpected error: %v", id, err)
	}
	return a, f
}

func checkCounts(t *testing.T, m *Maintainer, wantBridges, wantBlocks, wantComps int) {
	t.Helper()
	if got := m.BridgeCount(); got != wantBridges {
		t.Fatalf("BridgeCount=%d, want %d", got, wantBridges)
	}
	if got := m.BlockCount(); got != wantBlocks {
		t.Fatalf("BlockCount=%d, want %d", got, wantBlocks)
	}
	if got := m.ComponentCount(); got != wantComps {
		t.Fatalf("ComponentCount=%d, want %d", got, wantComps)
	}
}

// TestExampleSequence replays the worked example from the specification
// and checks every reported field.
func TestExampleSequence(t *testing.T) {
	m, err := New(6, 100)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Edges 1..4 are all Link and all become bridges.
	linkAdds := [][3]int{{0, 1, 5}, {1, 2, 3}, {2, 3, 4}, {4, 5, 2}}
	for i, a := range linkAdds {
		res := mustAdd(t, m, a[0], a[1], a[2])
		if res.ID != i+1 || res.Version != i+1 || res.Kind != KindLink {
			t.Fatalf("edge %d: got %+v, want Link id/version %d", i+1, res, i+1)
		}
		if len(res.Merged) != 0 || len(res.Unbridged) != 0 || res.NewWeight != 0 {
			t.Fatalf("edge %d: Link must not merge/unbridge anything: %+v", i+1, res)
		}
		if !mustIsBridge(t, m, res.ID) {
			t.Fatalf("edge %d must be a bridge", res.ID)
		}
	}
	checkCounts(t, m, 4, 6, 2)

	// After the first four edges MinBridge(0,3) is edge 2 with weight 3.
	id, w, err := m.MinBridge(0, 3)
	if err != nil || id != 2 || w != 3 {
		t.Fatalf("MinBridge(0,3)=(%d,%d,%v), want (2,3,nil)", id, w, err)
	}

	// Edge 5 parallels edge 4: Merge of blocks 4 and 5.
	res := mustAdd(t, m, 4, 5, 6)
	if res.Kind != KindMerge || res.ID != 5 || res.Version != 5 {
		t.Fatalf("edge 5: got %+v, want Merge id/version 5", res)
	}
	if !reflect.DeepEqual(res.Merged, []int{4, 5}) || res.NewLabel != 4 {
		t.Fatalf("edge 5: Merged=%v NewLabel=%d, want [4 5]/4", res.Merged, res.NewLabel)
	}
	if !reflect.DeepEqual(res.Unbridged, []int{4}) || res.NewWeight != 8 {
		t.Fatalf("edge 5: Unbridged=%v NewWeight=%d, want [4]/8", res.Unbridged, res.NewWeight)
	}
	for _, id := range []int{1, 2, 3} {
		if !mustIsBridge(t, m, id) {
			t.Fatalf("edge %d must still be a bridge", id)
		}
	}
	if mustIsBridge(t, m, 4) || mustIsBridge(t, m, 5) {
		t.Fatalf("edges 4 and 5 must be non-bridges")
	}
	if got := mustBlock(t, m, 5); got != 4 {
		t.Fatalf("Block(5)=%d, want 4", got)
	}

	// Edge 6 closes the chain 0-1-2-3 into a cycle.
	res = mustAdd(t, m, 0, 3, 7)
	if res.Kind != KindMerge || !reflect.DeepEqual(res.Merged, []int{0, 1, 2, 3}) ||
		res.NewLabel != 0 || !reflect.DeepEqual(res.Unbridged, []int{1, 2, 3}) ||
		res.NewWeight != 19 {
		t.Fatalf("edge 6: got %+v, want Merged=[0 1 2 3] NewLabel=0 Unbridged=[1 2 3] NewWeight=19", res)
	}
	checkCounts(t, m, 0, 2, 2)
	if got := mustWeight(t, m, 0); got != 19 {
		t.Fatalf("BlockWeight(0)=%d, want 19", got)
	}
	if got := mustWeight(t, m, 4); got != 8 {
		t.Fatalf("BlockWeight(4)=%d, want 8", got)
	}

	// Components {0..3} and {4,5} are not connected yet.
	if _, err := m.BridgesOnPath(0, 4); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("BridgesOnPath(0,4) err=%v, want ErrNotConnected", err)
	}

	// Edge 7 links the two components and is a bridge.
	res = mustAdd(t, m, 3, 4, 1)
	if res.Kind != KindLink || res.ID != 7 || !mustIsBridge(t, m, 7) {
		t.Fatalf("edge 7: got %+v, want Link id 7 as bridge", res)
	}
	if got := mustBridges(t, m, 0, 5); !reflect.DeepEqual(got, []int{7}) {
		t.Fatalf("BridgesOnPath(0,5)=%v, want [7]", got)
	}
	if got := mustBridges(t, m, 4, 5); len(got) != 0 {
		t.Fatalf("BridgesOnPath(4,5)=%v, want []", got)
	}
	if id, w, err := m.MinBridge(0, 5); err != nil || id != 7 || w != 1 {
		t.Fatalf("MinBridge(0,5)=(%d,%d,%v), want (7,1,nil)", id, w, err)
	}
	if got := mustWeight(t, m, 0); got != 19 {
		t.Fatalf("BlockWeight(0)=%d after Link, want 19", got)
	}
	if got := mustWeight(t, m, 4); got != 8 {
		t.Fatalf("BlockWeight(4)=%d after Link, want 8", got)
	}

	// Edge 8 merges the two blocks into one.
	res = mustAdd(t, m, 1, 5, 8)
	if res.Kind != KindMerge || !reflect.DeepEqual(res.Merged, []int{0, 4}) ||
		res.NewLabel != 0 || !reflect.DeepEqual(res.Unbridged, []int{7}) ||
		res.NewWeight != 36 {
		t.Fatalf("edge 8: got %+v, want Merged=[0 4] NewLabel=0 Unbridged=[7] NewWeight=36", res)
	}
	checkCounts(t, m, 0, 1, 1)
	if got := mustWeight(t, m, 5); got != 36 {
		t.Fatalf("BlockWeight(5)=%d, want 36", got)
	}

	// Edge 9 lands inside the single block.
	res = mustAdd(t, m, 2, 3, 2)
	if res.Kind != KindInside || res.NewWeight != 0 || len(res.Merged) != 0 ||
		len(res.Unbridged) != 0 {
		t.Fatalf("edge 9: got %+v, want Inside with empty reports", res)
	}
	if got := mustWeight(t, m, 0); got != 38 {
		t.Fatalf("BlockWeight(0)=%d, want 38", got)
	}
	if got := m.Version(); got != 9 {
		t.Fatalf("Version=%d, want 9", got)
	}

	// Per-edge history.
	wantHist := map[int][2]int{1: {1, 6}, 4: {4, 5}, 5: {5, 5}, 7: {7, 8}}
	for id, want := range wantHist {
		a, f := mustHistory(t, m, id)
		if a != want[0] || f != want[1] {
			t.Fatalf("EdgeHistory(%d)=(%d,%d), want (%d,%d)", id, a, f, want[0], want[1])
		}
	}
}

// TestInsideChangesNothing verifies that an Inside edge only grows the
// block weight and leaves every other piece of state untouched.
func TestInsideChangesNothing(t *testing.T) {
	m, _ := New(4, 10)
	mustAdd(t, m, 0, 1, 1)
	mustAdd(t, m, 1, 2, 1)
	mustAdd(t, m, 0, 2, 1) // Merge: triangle, one block {0,1,2}
	before := mustWeight(t, m, 0)
	res := mustAdd(t, m, 2, 0, 9)
	if res.Kind != KindInside || res.NewLabel != 0 || res.NewWeight != 0 ||
		len(res.Merged) != 0 || len(res.Unbridged) != 0 {
		t.Fatalf("Inside result: %+v", res)
	}
	checkCounts(t, m, 0, 2, 2) // block {0,1,2} plus isolated node 3
	if got := mustWeight(t, m, 1); got != before+9 {
		t.Fatalf("BlockWeight=%d, want %d", got, before+9)
	}
	if got := mustBlock(t, m, 2); got != 0 {
		t.Fatalf("Block(2)=%d, want 0", got)
	}
}

// TestParallelEdgeUnbridges checks that a parallel edge turns the
// earlier bridge into a non-bridge and merges the two blocks.
func TestParallelEdgeUnbridges(t *testing.T) {
	m, _ := New(2, 10)
	r1 := mustAdd(t, m, 0, 1, 5)
	if r1.Kind != KindLink || !mustIsBridge(t, m, 1) {
		t.Fatalf("first edge: %+v", r1)
	}
	r2 := mustAdd(t, m, 1, 0, 7)
	if r2.Kind != KindMerge || !reflect.DeepEqual(r2.Unbridged, []int{1}) ||
		!reflect.DeepEqual(r2.Merged, []int{0, 1}) || r2.NewLabel != 0 ||
		r2.NewWeight != 12 {
		t.Fatalf("second edge: %+v", r2)
	}
	if mustIsBridge(t, m, 1) || mustIsBridge(t, m, 2) {
		t.Fatalf("parallel edges must both be non-bridges")
	}
	checkCounts(t, m, 0, 1, 1)
}

// TestMinBridgeTieBreakAndAfterMerge checks the (weight, id) ordering
// and that unbridged edges leave the MinBridge candidate set.
func TestMinBridgeTieBreakAndAfterMerge(t *testing.T) {
	m, _ := New(4, 10)
	mustAdd(t, m, 0, 1, 5) // 1
	mustAdd(t, m, 1, 2, 5) // 2: same weight as 1, larger id
	mustAdd(t, m, 2, 3, 9) // 3
	id, w, err := m.MinBridge(0, 3)
	if err != nil || id != 1 || w != 5 {
		t.Fatalf("MinBridge=(%d,%d,%v), want (1,5,nil): tie must pick smaller id", id, w, err)
	}
	// Close the cycle 0-1-2-3-0: every bridge on the path dies.
	res := mustAdd(t, m, 3, 0, 1)
	if res.Kind != KindMerge || len(res.Unbridged) != 3 {
		t.Fatalf("res=%+v", res)
	}
	if _, _, err := m.MinBridge(0, 3); !errors.Is(err, ErrNoBridge) {
		t.Fatalf("MinBridge after merge err=%v, want ErrNoBridge", err)
	}
	if got := mustBridges(t, m, 0, 2); len(got) != 0 {
		t.Fatalf("BridgesOnPath after merge=%v, want []", got)
	}
}

// TestBlockWeightPerKind checks the weight rules for each kind.
func TestBlockWeightPerKind(t *testing.T) {
	m, _ := New(4, 10)
	mustAdd(t, m, 0, 1, 4) // 1: Link, bridge, no block weight
	if got := mustWeight(t, m, 0); got != 0 {
		t.Fatalf("Link must not change block weight, got %d", got)
	}
	mustAdd(t, m, 1, 0, 6) // 2: Merge via parallel edge
	if got := mustWeight(t, m, 0); got != 10 {
		t.Fatalf("after parallel merge weight=%d, want 10", got)
	}
	mustAdd(t, m, 0, 1, 3) // 3: Inside
	if got := mustWeight(t, m, 1); got != 13 {
		t.Fatalf("Inside must add its weight, got %d, want 13", got)
	}
	// Isolated nodes have weight 0.
	if got := mustWeight(t, m, 3); got != 0 {
		t.Fatalf("isolated node weight=%d, want 0", got)
	}
	// A Link edge keeps both blocks' weights unchanged.
	mustAdd(t, m, 2, 3, 8) // 4: Link
	if got := mustWeight(t, m, 2); got != 0 {
		t.Fatalf("Link changed weight: %d", got)
	}
}

// TestEdgeHistoryCases covers the three history shapes: born non-bridge,
// bridged-then-freed, and still a bridge.
func TestEdgeHistoryCases(t *testing.T) {
	m, _ := New(3, 10)
	mustAdd(t, m, 0, 1, 1) // 1: bridge, freed at version 3
	mustAdd(t, m, 1, 2, 1) // 2: bridge, freed at version 3
	mustAdd(t, m, 2, 0, 1) // 3: born non-bridge
	mustAdd(t, m, 0, 2, 1) // 4: born non-bridge (Inside)
	a, f := mustHistory(t, m, 1)
	if a != 1 || f != 3 {
		t.Fatalf("EdgeHistory(1)=(%d,%d), want (1,3)", a, f)
	}
	a, f = mustHistory(t, m, 3)
	if a != 3 || f != 3 {
		t.Fatalf("EdgeHistory(3)=(%d,%d), want (3,3)", a, f)
	}
	a, f = mustHistory(t, m, 4)
	if a != 4 || f != 4 {
		t.Fatalf("EdgeHistory(4)=(%d,%d), want (4,4)", a, f)
	}
	// Still-a-bridge case: fresh link edge.
	m2, _ := New(2, 5)
	mustAdd(t, m2, 0, 1, 1)
	a, f = mustHistory(t, m2, 1)
	if a != 1 || f != 0 {
		t.Fatalf("EdgeHistory still-bridge=(%d,%d), want (1,0)", a, f)
	}
}

// TestRejections checks the distinguishable failure reasons, their
// order, and that rejected calls consume neither ids nor versions.
func TestRejections(t *testing.T) {
	if _, err := New(0, 1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("New(0,1) err=%v", err)
	}
	if _, err := New(100001, 1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("New(100001,1) err=%v", err)
	}
	if _, err := New(1, 0); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("New(1,0) err=%v", err)
	}
	if _, err := New(1, 500001); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("New(1,500001) err=%v", err)
	}
	if _, err := New(1, 1); err != nil {
		t.Fatalf("New(1,1) should be legal: %v", err)
	}

	m, _ := New(3, 2)
	bad := [][3]int{
		{-1, 1, 1}, {0, 3, 1}, {0, 0, 1}, {1, 1, 5},
		{0, 1, 0}, {0, 1, -3}, {0, 1, 1000001},
	}
	for _, b := range bad {
		if _, err := m.AddEdge(b[0], b[1], b[2]); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("AddEdge%v err=%v, want ErrInvalidArgument", b, err)
		}
	}
	if m.Version() != 0 || m.EdgeCount() != 0 {
		t.Fatalf("rejected calls consumed id/version: v=%d e=%d", m.Version(), m.EdgeCount())
	}

	mustAdd(t, m, 0, 1, 1) // 1
	mustAdd(t, m, 1, 2, 1) // 2: limit reached
	// Invalid argument wins over the edge limit.
	if _, err := m.AddEdge(0, 0, 1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("full+invalid err=%v, want ErrInvalidArgument", err)
	}
	if _, err := m.AddEdge(0, 2, 1); !errors.Is(err, ErrEdgeLimit) {
		t.Fatalf("full err=%v, want ErrEdgeLimit", err)
	}
	if m.Version() != 2 || m.EdgeCount() != 2 {
		t.Fatalf("rejected calls changed state: v=%d e=%d", m.Version(), m.EdgeCount())
	}
	// Errors are distinguishable from each other.
	if errors.Is(ErrEdgeLimit, ErrInvalidArgument) || errors.Is(ErrNotConnected, ErrNoBridge) {
		t.Fatalf("error reasons must be distinguishable")
	}
}

// TestQueryErrors checks the failure order of the query endpoints.
func TestQueryErrors(t *testing.T) {
	m, _ := New(3, 5)
	mustAdd(t, m, 0, 1, 1)

	if _, err := m.IsBridge(0); !errors.Is(err, ErrNoSuchEdge) {
		t.Fatalf("IsBridge(0) err=%v", err)
	}
	if _, err := m.IsBridge(2); !errors.Is(err, ErrNoSuchEdge) {
		t.Fatalf("IsBridge(2) err=%v", err)
	}
	if _, _, err := m.EdgeHistory(9); !errors.Is(err, ErrNoSuchEdge) {
		t.Fatalf("EdgeHistory(9) err=%v", err)
	}
	if _, err := m.Block(3); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Block(3) err=%v", err)
	}
	if _, err := m.BlockWeight(-1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("BlockWeight(-1) err=%v", err)
	}
	if _, err := m.Connected(0, 7); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Connected(0,7) err=%v", err)
	}
	// Node validity beats connectivity.
	if _, err := m.BridgesOnPath(0, 99); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("BridgesOnPath(0,99) err=%v, want ErrInvalidArgument", err)
	}
	if _, _, err := m.MinBridge(0, 99); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("MinBridge(0,99) err=%v, want ErrInvalidArgument", err)
	}
	// Then connectivity.
	if _, err := m.BridgesOnPath(0, 2); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("BridgesOnPath(0,2) err=%v, want ErrNotConnected", err)
	}
	if _, _, err := m.MinBridge(0, 2); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("MinBridge(0,2) err=%v, want ErrNotConnected", err)
	}
	// Then the no-bridge case (same block).
	mustAdd(t, m, 1, 0, 1) // parallel: merges 0 and 1 into one block
	if _, _, err := m.MinBridge(0, 1); !errors.Is(err, ErrNoBridge) {
		t.Fatalf("MinBridge(0,1) err=%v, want ErrNoBridge", err)
	}
	if got := mustBridges(t, m, 0, 1); len(got) != 0 {
		t.Fatalf("BridgesOnPath(0,1)=%v, want []", got)
	}
}

// stepsBound is the amortized budget from the specification:
// N*(ceil(log2 N)+2) + 4*AddEdgeCalls.
func stepsBound(n, q int64) int64 {
	l := int64(0)
	for (int64(1) << l) < n {
		l++
	}
	return n*(l+2) + 4*q
}

// TestStepsLongChain links a long chain and closes it with one edge;
// the single closing merge must walk the whole chain exactly once.
func TestStepsLongChain(t *testing.T) {
	const n = 100000
	m, err := New(n, n)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for i := 0; i < n-1; i++ {
		res := mustAdd(t, m, i, i+1, 1)
		if res.Kind != KindLink {
			t.Fatalf("edge %d: kind=%s, want Link", i+1, res.Kind)
		}
	}
	res := mustAdd(t, m, 0, n-1, 1)
	if res.Kind != KindMerge || len(res.Unbridged) != n-1 || len(res.Merged) != n {
		t.Fatalf("closing merge: kind=%s unbridged=%d merged=%d",
			res.Kind, len(res.Unbridged), len(res.Merged))
	}
	checkCounts(t, m, 0, 1, 1)
	bound := stepsBound(n, n)
	t.Logf("chain: steps=%d bound=%d (N*(ceil(log2 N)+2)+4Q)", m.steps, bound)
	if m.steps > bound {
		t.Fatalf("steps=%d exceeds bound %d", m.steps, bound)
	}
}

// TestStepsRandomDense inserts 500k random edges on 100k nodes and
// checks the amortized step budget plus the structural invariants.
func TestStepsRandomDense(t *testing.T) {
	const n = 100000
	const e = 500000
	m, err := New(n, e)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	rng := rand.New(rand.NewSource(42))
	var totalW, linkW int64
	for i := 0; i < e; i++ {
		u := rng.Intn(n)
		v := rng.Intn(n)
		if u == v {
			v = (v + 1) % n
		}
		w := 1 + rng.Intn(1000000)
		res := mustAdd(t, m, u, v, w)
		totalW += int64(w)
		if res.Kind == KindLink {
			linkW += int64(w)
		}
	}
	bound := stepsBound(n, e)
	t.Logf("random 500k: steps=%d bound=%d bridges=%d blocks=%d comps=%d",
		m.steps, bound, m.BridgeCount(), m.BlockCount(), m.ComponentCount())
	if m.steps > bound {
		t.Fatalf("steps=%d exceeds bound %d", m.steps, bound)
	}

	// Invariants.
	if m.BlockCount() != m.BridgeCount()+m.ComponentCount() {
		t.Fatalf("forest invariant: blocks=%d bridges=%d comps=%d",
			m.BlockCount(), m.BridgeCount(), m.ComponentCount())
	}
	nonBridges := 0
	var bridgeW int64
	for id := 1; id <= e; id++ {
		b, err := m.IsBridge(id)
		if err != nil {
			t.Fatalf("IsBridge(%d): %v", id, err)
		}
		if b {
			bridgeW += m.ew[id]
		} else {
			nonBridges++
		}
	}
	if m.BridgeCount()+nonBridges != e {
		t.Fatalf("bridges+nonbridges=%d, want %d", m.BridgeCount()+nonBridges, e)
	}
	// Sum of block weights equals the total non-bridge weight.
	var blockWSum int64
	for x := 0; x < n; x++ {
		l := mustBlock(t, m, x)
		if l == x { // block label is its smallest node: count each block once
			blockWSum += mustWeight(t, m, x)
		}
	}
	if blockWSum != totalW-bridgeW {
		t.Fatalf("block weight sum=%d, non-bridge weight=%d", blockWSum, totalW-bridgeW)
	}
	_ = linkW
}

// TestReplayDeterminism runs the same script twice and demands
// identical reports and histories.
func TestReplayDeterminism(t *testing.T) {
	const n = 50
	const ops = 2000
	run := func() []AddResult {
		m, _ := New(n, ops)
		rng := rand.New(rand.NewSource(7))
		out := make([]AddResult, 0, ops)
		for i := 0; i < ops; i++ {
			u, v := rng.Intn(n), rng.Intn(n)
			if u == v {
				v = (v + 1) % n
			}
			res := mustAdd(t, m, u, v, 1+rng.Intn(1000))
			out = append(out, res)
		}
		// Fold the final per-edge history into the replay fingerprint.
		for id := 1; id <= ops; id++ {
			a, f := mustHistory(t, m, id)
			out = append(out, AddResult{ID: a, Version: f})
		}
		return out
	}
	first, second := run(), run()
	if !reflect.DeepEqual(first, second) {
		for i := range first {
			if !reflect.DeepEqual(first[i], second[i]) {
				t.Fatalf("replay diverged at record %d: %+v vs %+v", i, first[i], second[i])
			}
		}
	}
}

// TestConcurrent hammers the maintainer from many goroutines; with
// -race this proves the locking, and the final invariants prove that
// every observation matched some serial order.
func TestConcurrent(t *testing.T) {
	const n = 2000
	const adds = 20000
	m, err := New(n, adds)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	var adders, queries sync.WaitGroup
	for g := 0; g < 4; g++ {
		adders.Add(1)
		go func(seed int64) {
			defer adders.Done()
			rng := rand.New(rand.NewSource(seed))
			for i := 0; i < adds/4; i++ {
				u, v := rng.Intn(n), rng.Intn(n)
				if u == v {
					v = (v + 1) % n
				}
				if _, err := m.AddEdge(u, v, 1+rng.Intn(100)); err != nil {
					t.Errorf("AddEdge: %v", err)
					return
				}
			}
		}(int64(g))
	}
	stop := make(chan struct{})
	for g := 0; g < 4; g++ {
		queries.Add(1)
		go func(seed int64) {
			defer queries.Done()
			rng := rand.New(rand.NewSource(seed))
			for {
				select {
				case <-stop:
					return
				default:
				}
				u, v := rng.Intn(n), rng.Intn(n)
				_, _ = m.BridgesOnPath(u, v)
				_, _, _ = m.MinBridge(u, v)
				_, _ = m.Block(u)
				_, _ = m.BlockWeight(v)
				_, _ = m.Connected(u, v)
				// The forest invariant must hold at every observation;
				// read the counters under the lock for a consistent
				// snapshot.
				m.mu.Lock()
				consistent := m.blocks == m.bridges+m.comps
				m.mu.Unlock()
				if !consistent {
					t.Errorf("forest invariant violated under concurrency")
					return
				}
			}
		}(int64(100 + g))
	}
	adders.Wait()
	close(stop)
	queries.Wait()
	// All 20000 adds were accepted; the version matches the edge count.
	if m.Version() != adds || m.EdgeCount() != adds {
		t.Fatalf("version=%d edges=%d, want %d", m.Version(), m.EdgeCount(), adds)
	}
	if m.BlockCount() != m.BridgeCount()+m.ComponentCount() {
		t.Fatalf("final forest invariant broken")
	}
}

// TestBridgesOnPathAscendingAcrossBlocks checks that path reports are
// sorted and contain exactly the bridges on the path.
func TestBridgesOnPathAscendingAcrossBlocks(t *testing.T) {
	m, _ := New(7, 10)
	// Two triangles linked by a chain of two bridges.
	mustAdd(t, m, 0, 1, 1) // 1
	mustAdd(t, m, 1, 2, 1) // 2
	mustAdd(t, m, 0, 2, 1) // 3: block {0,1,2}
	mustAdd(t, m, 2, 3, 2) // 4: bridge
	mustAdd(t, m, 3, 4, 1) // 5: bridge
	mustAdd(t, m, 4, 5, 1) // 6
	mustAdd(t, m, 5, 6, 1) // 7
	mustAdd(t, m, 4, 6, 1) // 8: block {4,5,6}
	got := mustBridges(t, m, 0, 6)
	if !reflect.DeepEqual(got, []int{4, 5}) {
		t.Fatalf("BridgesOnPath(0,6)=%v, want [4 5]", got)
	}
	if got := mustBridges(t, m, 1, 4); !reflect.DeepEqual(got, []int{4, 5}) {
		t.Fatalf("BridgesOnPath(1,4)=%v, want [4 5]", got)
	}
	if got := mustBridges(t, m, 0, 2); len(got) != 0 {
		t.Fatalf("BridgesOnPath(0,2)=%v, want []", got)
	}
	id, w, err := m.MinBridge(0, 6)
	if err != nil || id != 5 || w != 1 {
		t.Fatalf("MinBridge(0,6)=(%d,%d,%v), want (5,1,nil)", id, w, err)
	}
}

// TestManyParallelEdges stresses multigraph parallelism.
func TestManyParallelEdges(t *testing.T) {
	m, _ := New(2, 10)
	mustAdd(t, m, 0, 1, 5)
	for i := 0; i < 5; i++ {
		res := mustAdd(t, m, 1, 0, 1)
		if res.Kind == KindLink {
			t.Fatalf("parallel edge %d reported Link", i+2)
		}
	}
	checkCounts(t, m, 0, 1, 1)
	if got := mustWeight(t, m, 0); got != 10 {
		t.Fatalf("BlockWeight=%d, want 10", got)
	}
	for id := 1; id <= 6; id++ {
		if mustIsBridge(t, m, id) {
			t.Fatalf("edge %d must not be a bridge", id)
		}
	}
}

// Example output guard: keep fmt imported for debugging helpers.
var _ = fmt.Sprintf

// TestCycleMergesAllBlocksOnPath builds a path of blocks and closes it
// into a ring, merging every block at once.
func TestCycleMergesAllBlocksOnPath(t *testing.T) {
	m, _ := New(5, 10)
	for i := 0; i < 4; i++ {
		mustAdd(t, m, i, i+1, 10+i)
	}
	checkCounts(t, m, 4, 5, 1)
	res := mustAdd(t, m, 4, 0, 100)
	if res.Kind != KindMerge {
		t.Fatalf("kind=%v, want Merge", res.Kind)
	}
	if !reflect.DeepEqual(res.Merged, []int{0, 1, 2, 3, 4}) || res.NewLabel != 0 {
		t.Fatalf("Merged=%v NewLabel=%d", res.Merged, res.NewLabel)
	}
	if !reflect.DeepEqual(res.Unbridged, []int{1, 2, 3, 4}) {
		t.Fatalf("Unbridged=%v, want [1 2 3 4]", res.Unbridged)
	}
	wantWeight := int64(10 + 11 + 12 + 13 + 100)
	if res.NewWeight != wantWeight {
		t.Fatalf("NewWeight=%d, want %d", res.NewWeight, wantWeight)
	}
	checkCounts(t, m, 0, 1, 1)
}

// TestMergeLabelIsMinNode makes the merged label come from a node that
// is not an endpoint of the closing edge.
func TestMergeLabelIsMinNode(t *testing.T) {
	m, _ := New(6, 10)
	// Chain 5-0-4 and 1-2-3, then link the chains, then close a cycle
	// whose smallest node (0) sits in the middle of the path.
	mustAdd(t, m, 5, 0, 1) // 1
	mustAdd(t, m, 0, 4, 1) // 2
	mustAdd(t, m, 1, 2, 1) // 3
	mustAdd(t, m, 2, 3, 1) // 4
	mustAdd(t, m, 4, 1, 1) // 5: link the two chains
	res := mustAdd(t, m, 3, 5, 1)
	if res.Kind != KindMerge || res.NewLabel != 0 {
		t.Fatalf("res=%+v, want Merge with NewLabel 0", res)
	}
	if !reflect.DeepEqual(res.Merged, []int{0, 1, 2, 3, 4, 5}) {
		t.Fatalf("Merged=%v", res.Merged)
	}
	checkCounts(t, m, 0, 1, 1)
}

// TestBridgeOffPathStaysBridge verifies that bridges not on the merged
// path are unaffected.
func TestBridgeOffPathStaysBridge(t *testing.T) {
	m, _ := New(5, 10)
	mustAdd(t, m, 0, 1, 1)        // 1
	mustAdd(t, m, 1, 2, 1)        // 2
	mustAdd(t, m, 2, 3, 1)        // 3: side branch
	mustAdd(t, m, 2, 4, 1)        // 4
	res := mustAdd(t, m, 4, 0, 1) // closes cycle 0-1-2-4
	if res.Kind != KindMerge || !reflect.DeepEqual(res.Unbridged, []int{1, 2, 4}) {
		t.Fatalf("res=%+v, want Unbridged [1 2 4]", res)
	}
	if !mustIsBridge(t, m, 3) {
		t.Fatalf("edge 3 off the cycle must remain a bridge")
	}
	checkCounts(t, m, 1, 2, 1)
	if got := mustBridges(t, m, 0, 3); !reflect.DeepEqual(got, []int{3}) {
		t.Fatalf("BridgesOnPath(0,3)=%v, want [3]", got)
	}
}

// TestLinkThenMergeAcrossComponents connects two cyclic components with
// a bridge and then merges everything with one more edge.
func TestLinkThenMergeAcrossComponents(t *testing.T) {
	m, _ := New(6, 10)
	// Component A: triangle 0-1-2; component B: triangle 3-4-5.
	mustAdd(t, m, 0, 1, 2)
	mustAdd(t, m, 1, 2, 2)
	mustAdd(t, m, 0, 2, 2)
	mustAdd(t, m, 3, 4, 3)
	mustAdd(t, m, 4, 5, 3)
	mustAdd(t, m, 3, 5, 3)
	checkCounts(t, m, 0, 2, 2)

	link := mustAdd(t, m, 2, 3, 5) // 7: bridge between the blocks
	if link.Kind != KindLink || !mustIsBridge(t, m, 7) {
		t.Fatalf("link: %+v", link)
	}
	checkCounts(t, m, 1, 2, 1)

	merge := mustAdd(t, m, 0, 5, 7) // 8: merges both blocks
	if merge.Kind != KindMerge || !reflect.DeepEqual(merge.Unbridged, []int{7}) {
		t.Fatalf("merge: %+v", merge)
	}
	if !reflect.DeepEqual(merge.Merged, []int{0, 3}) || merge.NewLabel != 0 {
		t.Fatalf("Merged=%v NewLabel=%d", merge.Merged, merge.NewLabel)
	}
	want := int64(2 + 2 + 2 + 3 + 3 + 3 + 5 + 7)
	if merge.NewWeight != want {
		t.Fatalf("NewWeight=%d, want %d", merge.NewWeight, want)
	}
	checkCounts(t, m, 0, 1, 1)
}
