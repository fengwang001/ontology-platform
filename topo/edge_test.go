package topo

import (
	"errors"
	"reflect"
	"testing"
)

// TestNoReorderWhenOrdered: ord(u) < ord(v) adds the edge without touching
// any order value and marks zero nodes.
func TestNoReorderWhenOrdered(t *testing.T) {
	m := New(10, 100)
	mustAddNodes(t, m, 4)
	mustAddEdge(t, m, 0, 3, nil, 0, 0)
	mustAddEdge(t, m, 1, 2, nil, 0, 0)
	mustOrder(t, m, []int{0, 1, 2, 3})
	if got := m.Touched(); got != 0 {
		t.Fatalf("touched = %d, want 0", got)
	}
}

// TestAdjacentSwap: ord(u) exactly one above ord(v) swaps the two nodes.
func TestAdjacentSwap(t *testing.T) {
	m := New(10, 100)
	mustAddNodes(t, m, 3)
	mustAddEdge(t, m, 1, 0, []int{1, 0}, 1, 1)
	mustOrder(t, m, []int{1, 0, 2})
	mustOrd(t, m, 1, 0)
	mustOrd(t, m, 0, 1)
}

// TestTruncationAndPool: deltaF is truncated by ub, deltaB by lb, deltaB is
// allocated before deltaF, and several nodes move at once.
//
// Graph: 0->1->2->5, 0->3->4->5. AddEdge(4,1): lb=1, ub=4.
// deltaF from 1: {1,2} (5 has ord 5 > ub, excluded).
// deltaB from 4: {4,3} (0 has ord 0 < lb, excluded).
// Pool [1,2,3,4]; sequence [3,4,1,2] receives 1,2,3,4.
func TestTruncationAndPool(t *testing.T) {
	m := New(10, 100)
	mustAddNodes(t, m, 6)
	for _, e := range [][2]int{{0, 1}, {1, 2}, {2, 5}, {0, 3}, {3, 4}, {4, 5}} {
		mustAddEdge(t, m, e[0], e[1], nil, 0, 0)
	}
	mustAddEdge(t, m, 4, 1, []int{3, 4, 1, 2}, 2, 2)
	mustOrd(t, m, 0, 0)
	mustOrd(t, m, 3, 1)
	mustOrd(t, m, 4, 2)
	mustOrd(t, m, 1, 3)
	mustOrd(t, m, 2, 4)
	mustOrd(t, m, 5, 5)
	mustOrder(t, m, []int{0, 3, 4, 1, 2, 5})
}

// TestWitnessLexicographic: among the two shortest paths 0->1->3 and
// 0->2->3 the witness picks the lexicographically smaller one.
func TestWitnessLexicographic(t *testing.T) {
	m := New(10, 100)
	mustAddNodes(t, m, 4)
	for _, e := range [][2]int{{0, 1}, {0, 2}, {1, 3}, {2, 3}} {
		mustAddEdge(t, m, e[0], e[1], nil, 0, 0)
	}
	mustCycle(t, m, 3, 0, []int{0, 1, 3})
}

// TestWitnessShortest: the witness uses the fewest edges even when a longer
// path with smaller ids exists.
func TestWitnessShortest(t *testing.T) {
	m := New(10, 100)
	mustAddNodes(t, m, 5)
	// Short path 0->4, long path 0->1->2->3->4.
	for _, e := range [][2]int{{0, 4}, {0, 1}, {1, 2}, {2, 3}, {3, 4}} {
		mustAddEdge(t, m, e[0], e[1], nil, 0, 0)
	}
	mustCycle(t, m, 4, 0, []int{0, 4})
}

// TestErrorPrecedenceAddEdge checks the rejection order: node missing, edge
// exists, edge limit, cycle.
func TestErrorPrecedenceAddEdge(t *testing.T) {
	m := New(2, 1)
	mustAddNodes(t, m, 2)
	mustAddEdge(t, m, 0, 1, nil, 0, 0)

	if _, err := m.AddEdge(9, 0); !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("AddEdge(9,0) = %v, want ErrNodeNotFound", err)
	}
	if _, err := m.AddEdge(0, 9); !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("AddEdge(0,9) = %v, want ErrNodeNotFound", err)
	}
	// Missing node wins over the self-loop cycle reason.
	if _, err := m.AddEdge(9, 9); !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("AddEdge(9,9) = %v, want ErrNodeNotFound", err)
	}
	// Duplicate wins over the edge limit.
	if _, err := m.AddEdge(0, 1); !errors.Is(err, ErrEdgeExists) {
		t.Fatalf("AddEdge(0,1) = %v, want ErrEdgeExists", err)
	}
	// Edge limit wins over the cycle reason (1->0 would close a cycle).
	if _, err := m.AddEdge(1, 0); !errors.Is(err, ErrEdgeLimit) {
		t.Fatalf("AddEdge(1,0) = %v, want ErrEdgeLimit", err)
	}
	// Self loop on an existing node reports a cycle even at the edge limit.
	var ce *CycleError
	if _, err := m.AddEdge(0, 0); !errors.As(err, &ce) || !reflect.DeepEqual(ce.Path, []int{0}) {
		t.Fatalf("AddEdge(0,0) = %v, want CycleError [0]", err)
	}
}

// TestErrorPrecedenceOthers covers RemoveEdge/RemoveNode/AddNode reasons.
func TestErrorPrecedenceOthers(t *testing.T) {
	m := New(1, 5)
	mustAddNodes(t, m, 1)
	if _, err := m.AddNode(); !errors.Is(err, ErrNodeLimit) {
		t.Fatalf("AddNode = %v, want ErrNodeLimit", err)
	}
	if err := m.RemoveEdge(9, 0); !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("RemoveEdge(9,0) = %v, want ErrNodeNotFound", err)
	}
	if err := m.RemoveEdge(0, 9); !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("RemoveEdge(0,9) = %v, want ErrNodeNotFound", err)
	}
	if err := m.RemoveEdge(0, 0); !errors.Is(err, ErrEdgeNotFound) {
		t.Fatalf("RemoveEdge(0,0) = %v, want ErrEdgeNotFound", err)
	}
	if err := m.RemoveNode(9); !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("RemoveNode(9) = %v, want ErrNodeNotFound", err)
	}
	if _, err := m.OrdOf(9); !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("OrdOf(9) = %v, want ErrNodeNotFound", err)
	}
}

// TestRemoveEdgeReverse: after removing an edge the reverse edge can be
// added (with a reordering).
func TestRemoveEdgeReverse(t *testing.T) {
	m := New(10, 100)
	mustAddNodes(t, m, 2)
	mustAddEdge(t, m, 0, 1, nil, 0, 0)
	if err := m.RemoveEdge(0, 1); err != nil {
		t.Fatalf("RemoveEdge(0,1): %v", err)
	}
	mustAddEdge(t, m, 1, 0, []int{1, 0}, 1, 1)
	mustOrder(t, m, []int{1, 0})
	// And back again.
	if err := m.RemoveEdge(1, 0); err != nil {
		t.Fatalf("RemoveEdge(1,0): %v", err)
	}
	mustAddEdge(t, m, 0, 1, []int{0, 1}, 1, 1)
	mustOrder(t, m, []int{0, 1})
}

// TestRejectedKeepsState: every kind of rejection leaves order values, the
// edge set, the creation counter and touched untouched.
func TestRejectedKeepsState(t *testing.T) {
	m := New(4, 4)
	mustAddNodes(t, m, 4)
	mustAddEdge(t, m, 0, 1, nil, 0, 0)
	// deltaF from 0 includes node 1 via edge 0->1 (ord 1 <= ub 2).
	mustAddEdge(t, m, 2, 0, []int{2, 0, 1}, 2, 1)
	mustAddEdge(t, m, 1, 3, nil, 0, 0)
	mustOrder(t, m, []int{2, 0, 1, 3})

	reject := func(op func() error, want error) {
		t.Helper()
		before := m.Order()
		touched := m.Touched()
		err := op()
		if want != nil && !errors.Is(err, want) {
			t.Fatalf("err = %v, want %v", err, want)
		}
		if want == nil && err == nil {
			t.Fatal("operation unexpectedly succeeded")
		}
		if got := m.Order(); !reflect.DeepEqual(got, before) {
			t.Fatalf("Order changed to %v after rejection", got)
		}
		if got := m.Touched(); got != touched {
			t.Fatalf("touched changed to %d after rejection", got)
		}
	}
	reject(func() error { _, err := m.AddEdge(9, 0); return err }, ErrNodeNotFound)
	reject(func() error { _, err := m.AddEdge(0, 1); return err }, ErrEdgeExists)
	reject(func() error { _, err := m.AddEdge(1, 2); return err }, nil) // cycle 2->0->1
	reject(func() error { _, err := m.AddEdge(3, 3); return err }, nil) // self loop
	reject(func() error { return m.RemoveEdge(1, 0) }, ErrEdgeNotFound)
	reject(func() error { return m.RemoveNode(9) }, ErrNodeNotFound)
	reject(func() error { _, err := m.AddEdges(nil); return err }, ErrBatchSize)
	reject(func() error { _, err := m.AddEdges(make([][2]int, 1001)); return err }, ErrBatchSize)
	reject(func() error { _, err := m.AddEdges([][2]int{{3, 0}, {0, 3}}); return err }, nil)

	// Fill the edge limit, then the limit reason must not change state.
	mustAddEdge(t, m, 2, 3, nil, 0, 0)
	reject(func() error { _, err := m.AddEdge(3, 0); return err }, ErrEdgeLimit)
}

// TestBatchDependencies: entries see earlier entries of the same batch.
func TestBatchDependencies(t *testing.T) {
	// Duplicate edge inside one batch.
	m := New(10, 100)
	mustAddNodes(t, m, 3)
	_, err := m.AddEdges([][2]int{{0, 1}, {0, 1}})
	var be *BatchError
	if !errors.As(err, &be) || be.Index != 1 || !errors.Is(be.Reason, ErrEdgeExists) {
		t.Fatalf("err = %v, want BatchError{1, ErrEdgeExists}", err)
	}
	mustOrder(t, m, []int{0, 1, 2})
	if got := m.Touched(); got != 0 {
		t.Fatalf("touched = %d, want 0", got)
	}

	// Earlier entry fills the edge limit for a later entry.
	m2 := New(10, 2)
	mustAddNodes(t, m2, 3)
	_, err = m2.AddEdges([][2]int{{0, 1}, {1, 2}, {0, 2}})
	if !errors.As(err, &be) || be.Index != 2 || !errors.Is(be.Reason, ErrEdgeLimit) {
		t.Fatalf("err = %v, want BatchError{2, ErrEdgeLimit}", err)
	}
	mustOrder(t, m2, []int{0, 1, 2})
	if _, err := m2.AddEdge(0, 1); err != nil {
		t.Fatalf("AddEdge(0,1) after rollback: %v", err)
	}

	// Earlier entry reorders so that a later entry adds without reordering.
	m3 := New(10, 100)
	mustAddNodes(t, m3, 3)
	res, err := m3.AddEdges([][2]int{{2, 0}, {2, 1}})
	if err != nil {
		t.Fatalf("AddEdges: %v", err)
	}
	wantCounts := [][2]int{{1, 1}, {0, 0}}
	if !reflect.DeepEqual(res.Counts, wantCounts) {
		t.Fatalf("Counts = %v, want %v", res.Counts, wantCounts)
	}
	if !reflect.DeepEqual(res.Moved, []int{2, 0}) {
		t.Fatalf("Moved = %v, want [2 0]", res.Moved)
	}
	mustOrder(t, m3, []int{2, 1, 0})
}

// TestBatchSizeIllegal rejects empty and oversized batches.
func TestBatchSizeIllegal(t *testing.T) {
	m := New(10, 100)
	if _, err := m.AddEdges(nil); !errors.Is(err, ErrBatchSize) {
		t.Fatalf("empty batch = %v, want ErrBatchSize", err)
	}
	if _, err := m.AddEdges(make([][2]int, 1001)); !errors.Is(err, ErrBatchSize) {
		t.Fatalf("oversized batch = %v, want ErrBatchSize", err)
	}
}

// TestTouchedLongChain: on a chain of 100000 nodes, adding an edge between
// adjacent order values marks exactly 2 nodes. (Order values are distinct,
// so deltaF={v} and deltaB={u} whenever ord(u)=ord(v)+1 and the edge is
// accepted; the reverse edge of a chain edge becomes addable after removing
// the chain edge.)
func TestTouchedLongChain(t *testing.T) {
	const n = 100000
	m := New(n, n)
	mustAddNodes(t, m, n)
	for i := 0; i+1 < n; i++ {
		if _, err := m.AddEdge(i, i+1); err != nil {
			t.Fatalf("AddEdge(%d,%d): %v", i, i+1, err)
		}
	}
	if got := m.Touched(); got != 0 {
		t.Fatalf("touched = %d after building the chain, want 0", got)
	}
	// Reverse the middle chain edge: ord(k+1)=ord(k)+1, adjacent order values.
	for _, k := range []int{0, 1, n / 2, n - 3, n - 2} {
		if err := m.RemoveEdge(k, k+1); err != nil {
			t.Fatalf("RemoveEdge(%d,%d): %v", k, k+1, err)
		}
		before := m.Touched()
		res, err := m.AddEdge(k+1, k)
		if err != nil {
			t.Fatalf("AddEdge(%d,%d): %v", k+1, k, err)
		}
		if got := m.Touched() - before; got != 2 {
			t.Fatalf("AddEdge(%d,%d) touched %d nodes, want 2", k+1, k, got)
		}
		if res.DeltaF != 1 || res.DeltaB != 1 {
			t.Fatalf("AddEdge(%d,%d) counts = (%d,%d), want (1,1)", k+1, k, res.DeltaF, res.DeltaB)
		}
		if !reflect.DeepEqual(res.Moved, []int{k + 1, k}) {
			t.Fatalf("AddEdge(%d,%d) Moved = %v, want [%d %d]", k+1, k, res.Moved, k+1, k)
		}
		// Restore the chain for the next position.
		if err := m.RemoveEdge(k+1, k); err != nil {
			t.Fatalf("RemoveEdge(%d,%d): %v", k+1, k, err)
		}
		if _, err := m.AddEdge(k, k+1); err != nil {
			t.Fatalf("AddEdge(%d,%d): %v", k, k+1, err)
		}
	}
}
