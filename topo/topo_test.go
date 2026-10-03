package topo

import (
	"errors"
	"reflect"
	"testing"
)

func mustAddNodes(t *testing.T, m *Maintainer, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		id, err := m.AddNode()
		if err != nil {
			t.Fatalf("AddNode %d: %v", i, err)
		}
		if id != i {
			t.Fatalf("AddNode: got id %d, want %d", id, i)
		}
	}
}

func mustOrder(t *testing.T, m *Maintainer, want []int) {
	t.Helper()
	if got := m.Order(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Order = %v, want %v", got, want)
	}
}

func mustAddEdge(t *testing.T, m *Maintainer, u, v int, wantMoved []int, wantF, wantB int) {
	t.Helper()
	res, err := m.AddEdge(u, v)
	if err != nil {
		t.Fatalf("AddEdge(%d,%d): %v", u, v, err)
	}
	if !reflect.DeepEqual(res.Moved, wantMoved) {
		t.Fatalf("AddEdge(%d,%d) Moved = %v, want %v", u, v, res.Moved, wantMoved)
	}
	if res.DeltaF != wantF || res.DeltaB != wantB {
		t.Fatalf("AddEdge(%d,%d) counts = (%d,%d), want (%d,%d)", u, v, res.DeltaF, res.DeltaB, wantF, wantB)
	}
}

func mustCycle(t *testing.T, m *Maintainer, u, v int, wantPath []int) {
	t.Helper()
	_, err := m.AddEdge(u, v)
	var ce *CycleError
	if !errors.As(err, &ce) {
		t.Fatalf("AddEdge(%d,%d): err = %v, want CycleError", u, v, err)
	}
	if !reflect.DeepEqual(ce.Path, wantPath) {
		t.Fatalf("AddEdge(%d,%d) witness = %v, want %v", u, v, ce.Path, wantPath)
	}
}

func mustOrd(t *testing.T, m *Maintainer, x, want int) {
	t.Helper()
	got, err := m.OrdOf(x)
	if err != nil {
		t.Fatalf("OrdOf(%d): %v", x, err)
	}
	if got != want {
		t.Fatalf("OrdOf(%d) = %d, want %d", x, got, want)
	}
}

// TestSpecExample replays the worked example from the specification.
func TestSpecExample(t *testing.T) {
	m := New(100000, 500000)
	mustAddNodes(t, m, 5)

	// AddEdge(3,1): lb=1, ub=3, deltaF=[1], deltaB=[3], pool [1,3].
	mustAddEdge(t, m, 3, 1, []int{3, 1}, 1, 1)
	mustOrder(t, m, []int{0, 3, 2, 1, 4})

	// AddEdge(1,2): ord(1)=3 > ord(2)=2, node 3 (ord 1 < lb) stays out.
	mustAddEdge(t, m, 1, 2, []int{1, 2}, 1, 1)
	mustOrder(t, m, []int{0, 3, 1, 2, 4})

	// AddEdge(2,3): cycle, witness [3,1,2].
	mustCycle(t, m, 2, 3, []int{3, 1, 2})

	// AddEdge(4,0): direct swap of adjacent order values.
	mustAddEdge(t, m, 4, 0, []int{4, 0}, 1, 1)
	mustOrder(t, m, []int{4, 3, 1, 2, 0})

	// AddEdge(0,1): deltaF=[1,2], deltaB=[0], pool [2,3,4].
	mustAddEdge(t, m, 0, 1, []int{0, 1, 2}, 2, 1)
	mustOrder(t, m, []int{4, 3, 0, 1, 2})

	// AddEdge(2,2): self loop, witness [2].
	mustCycle(t, m, 2, 2, []int{2})

	// RemoveNode(1) removes its three incident edges; ord holes remain.
	if err := m.RemoveNode(1); err != nil {
		t.Fatalf("RemoveNode(1): %v", err)
	}
	mustOrder(t, m, []int{4, 3, 0, 2})
	mustOrd(t, m, 2, 4)

	// New node gets id 5 and order value 5 (ids are never reused).
	id, err := m.AddNode()
	if err != nil {
		t.Fatalf("AddNode: %v", err)
	}
	if id != 5 {
		t.Fatalf("AddNode id = %d, want 5", id)
	}
	mustOrd(t, m, 5, 5)
	mustOrder(t, m, []int{4, 3, 0, 2, 5})
}

// TestSpecBatchExample replays the batch example from the specification.
func TestSpecBatchExample(t *testing.T) {
	m := New(100000, 500000)
	mustAddNodes(t, m, 5)
	res, err := m.AddEdges([][2]int{{1, 2}, {3, 1}, {1, 0}})
	if err != nil {
		t.Fatalf("AddEdges: %v", err)
	}
	// Node 1 moved mid-batch but returned to its original order value.
	if !reflect.DeepEqual(res.Moved, []int{3, 0, 2}) {
		t.Fatalf("batch Moved = %v, want [3 0 2]", res.Moved)
	}
	wantCounts := [][2]int{{0, 0}, {2, 1}, {1, 2}}
	if !reflect.DeepEqual(res.Counts, wantCounts) {
		t.Fatalf("batch Counts = %v, want %v", res.Counts, wantCounts)
	}
	mustOrder(t, m, []int{3, 1, 0, 2, 4})
	if got, want := m.Touched(), int64(6); got != want {
		t.Fatalf("touched = %d, want %d", got, want)
	}
}

// TestSpecBatchRollback replays the failing batch example: the cycle at
// index 1 rolls the whole batch back.
func TestSpecBatchRollback(t *testing.T) {
	m := New(100000, 500000)
	mustAddNodes(t, m, 5)
	_, err := m.AddEdges([][2]int{{1, 2}, {2, 1}})
	var be *BatchError
	if !errors.As(err, &be) {
		t.Fatalf("err = %v, want BatchError", err)
	}
	if be.Index != 1 {
		t.Fatalf("batch error index = %d, want 1", be.Index)
	}
	var ce *CycleError
	if !errors.As(be.Reason, &ce) {
		t.Fatalf("batch reason = %v, want CycleError", be.Reason)
	}
	if !reflect.DeepEqual(ce.Path, []int{1, 2}) {
		t.Fatalf("witness = %v, want [1 2]", ce.Path)
	}
	mustOrder(t, m, []int{0, 1, 2, 3, 4})
	if got := m.Touched(); got != 0 {
		t.Fatalf("touched = %d, want 0 after rollback", got)
	}
	// No edge survived the rollback: (1,2) can be added again.
	if _, err := m.AddEdge(1, 2); err != nil {
		t.Fatalf("AddEdge(1,2) after rollback: %v", err)
	}
}
