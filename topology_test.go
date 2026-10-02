package ontology

import (
	"errors"
	"reflect"
	"testing"
)

func mustAddNodes(t *testing.T, m *Maintainer, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		x, err := m.AddNode()
		if err != nil || x != i {
			t.Fatalf("AddNode #%d = (%d,%v), want (%d,nil)", i, x, err, i)
		}
	}
}

func assertOrder(t *testing.T, m *Maintainer, want []int) {
	t.Helper()
	if got := m.Order(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Order = %v, want %v", got, want)
	}
}

func addEdgeOK(t *testing.T, m *Maintainer, u, v int, want AddEdgeResult) {
	t.Helper()
	got, err := m.AddEdge(u, v)
	if err != nil {
		t.Fatalf("AddEdge(%d,%d): %v", u, v, err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("AddEdge(%d,%d) = %+v, want %+v", u, v, got, want)
	}
}

func cycleWitness(t *testing.T, err error, want []int) {
	t.Helper()
	var cycle *CycleError
	if !errors.As(err, &cycle) {
		t.Fatalf("error = %v, want cycle", err)
	}
	if !reflect.DeepEqual(cycle.Witness, want) {
		t.Fatalf("witness = %v, want %v", cycle.Witness, want)
	}
}

func TestSpecificationExample(t *testing.T) {
	m := NewMaintainer(10, 20)
	mustAddNodes(t, m, 5)

	addEdgeOK(t, m, 3, 1, AddEdgeResult{Moved: []int{3, 1}, Forward: 1, Backward: 1})
	assertOrder(t, m, []int{0, 3, 2, 1, 4})

	addEdgeOK(t, m, 1, 2, AddEdgeResult{Moved: []int{1, 2}, Forward: 1, Backward: 1})
	assertOrder(t, m, []int{0, 3, 1, 2, 4})

	_, err := m.AddEdge(2, 3)
	cycleWitness(t, err, []int{3, 1, 2})

	addEdgeOK(t, m, 4, 0, AddEdgeResult{Moved: []int{4, 0}, Forward: 1, Backward: 1})
	assertOrder(t, m, []int{4, 3, 1, 2, 0})

	addEdgeOK(t, m, 0, 1, AddEdgeResult{Moved: []int{0, 1, 2}, Forward: 2, Backward: 1})
	assertOrder(t, m, []int{4, 3, 0, 1, 2})

	_, err = m.AddEdge(2, 2)
	cycleWitness(t, err, []int{2})

	if err := m.RemoveNode(1); err != nil {
		t.Fatal(err)
	}
	assertOrder(t, m, []int{4, 3, 0, 2})
	if ord, ok := m.OrdOf(2); !ok || ord != 4 {
		t.Fatalf("OrdOf(2) = (%d,%v), want (4,true)", ord, ok)
	}
	x, err := m.AddNode()
	if err != nil || x != 5 {
		t.Fatalf("AddNode after hole = (%d,%v), want (5,nil)", x, err)
	}
	if ord, ok := m.OrdOf(5); !ok || ord != 5 {
		t.Fatalf("OrdOf(5) = (%d,%v), want (5,true)", ord, ok)
	}
}

func TestNoMoveWhenForward(t *testing.T) {
	m := NewMaintainer(10, 10)
	mustAddNodes(t, m, 3)
	addEdgeOK(t, m, 0, 2, AddEdgeResult{})
	if got := m.Touched(); got != 0 {
		t.Fatalf("Touched = %d, want 0", got)
	}
	assertOrder(t, m, []int{0, 1, 2})
}

func TestAdjacentSwapAndTruncation(t *testing.T) {
	m := NewMaintainer(10, 20)
	mustAddNodes(t, m, 5)
	addEdgeOK(t, m, 1, 4, AddEdgeResult{})
	addEdgeOK(t, m, 2, 4, AddEdgeResult{})
	addEdgeOK(t, m, 2, 1, AddEdgeResult{Moved: []int{2, 1}, Forward: 1, Backward: 1})
	assertOrder(t, m, []int{0, 2, 1, 3, 4})
}

func TestMultiNodeRearrangement(t *testing.T) {
	m := NewMaintainer(10, 20)
	mustAddNodes(t, m, 5)
	addEdgeOK(t, m, 3, 2, AddEdgeResult{Moved: []int{3, 2}, Forward: 1, Backward: 1})
	addEdgeOK(t, m, 4, 3, AddEdgeResult{Moved: []int{4, 3, 2}, Forward: 2, Backward: 1})
	addEdgeOK(t, m, 3, 1, AddEdgeResult{Moved: []int{4, 3, 1}, Forward: 1, Backward: 2})
	assertOrder(t, m, []int{0, 4, 3, 1, 2})

	addEdgeOK(t, m, 1, 0, AddEdgeResult{Moved: []int{4, 3, 1, 0}, Forward: 1, Backward: 3})
	assertOrder(t, m, []int{4, 3, 1, 0, 2})
}

func TestBackwardLowerBoundTruncation(t *testing.T) {
	m := NewMaintainer(10, 20)
	mustAddNodes(t, m, 5)
	addEdgeOK(t, m, 4, 3, AddEdgeResult{Moved: []int{4, 3}, Forward: 1, Backward: 1})
	addEdgeOK(t, m, 1, 0, AddEdgeResult{Moved: []int{1, 0}, Forward: 1, Backward: 1})
	assertOrder(t, m, []int{1, 0, 2, 4, 3})

	addEdgeOK(t, m, 3, 2, AddEdgeResult{Moved: []int{4, 3, 2}, Forward: 1, Backward: 2})
	assertOrder(t, m, []int{1, 0, 4, 3, 2})
}

func TestLexicographicallySmallestWitness(t *testing.T) {
	m := NewMaintainer(20, 40)
	mustAddNodes(t, m, 9)
	for _, e := range [][2]int{{0, 1}, {1, 7}, {2, 3}, {3, 7}, {4, 5}, {5, 6}, {6, 8}, {7, 8}} {
		addEdgeOK(t, m, e[0], e[1], AddEdgeResult{})
	}
	_, err := m.AddEdge(8, 0)
	cycleWitness(t, err, []int{0, 1, 7, 8})
}

func TestRejectionsDoNotMutate(t *testing.T) {
	m := NewMaintainer(2, 2)
	mustAddNodes(t, m, 2)
	addEdgeOK(t, m, 0, 1, AddEdgeResult{})
	before := m.Order()

	if _, err := m.AddEdge(0, 1); !errors.Is(err, ErrEdgeAlreadyExists) {
		t.Fatalf("duplicate: %v", err)
	}
	_, err := m.AddEdge(1, 0)
	cycleWitness(t, err, []int{0, 1})
	if _, err := m.AddEdge(0, 5); !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("missing node: %v", err)
	}
	if !reflect.DeepEqual(m.Order(), before) || m.Touched() != 0 {
		t.Fatalf("state changed: order=%v touched=%d", m.Order(), m.Touched())
	}
}

func TestRemoveEdgeAllowsReverse(t *testing.T) {
	m := NewMaintainer(3, 3)
	mustAddNodes(t, m, 3)
	addEdgeOK(t, m, 0, 1, AddEdgeResult{})
	if err := m.RemoveEdge(0, 1); err != nil {
		t.Fatal(err)
	}
	addEdgeOK(t, m, 1, 0, AddEdgeResult{Moved: []int{1, 0}, Forward: 1, Backward: 1})
	assertOrder(t, m, []int{1, 0, 2})
}

func TestLimitsAndRemoveMissing(t *testing.T) {
	m := NewMaintainer(1, 1)
	x, err := m.AddNode()
	if err != nil || x != 0 {
		t.Fatalf("AddNode = (%d,%v)", x, err)
	}
	if _, err := m.AddNode(); !errors.Is(err, ErrNodeLimit) {
		t.Fatalf("node limit: %v", err)
	}
	if _, err := m.AddEdge(0, 0); err == nil {
		t.Fatal("self-loop unexpectedly accepted")
	}
	if err := m.RemoveEdge(0, 0); !errors.Is(err, ErrEdgeNotFound) {
		t.Fatalf("RemoveEdge self: %v", err)
	}
	if err := m.RemoveNode(9); !errors.Is(err, ErrNodeNotFound) {
		t.Fatalf("RemoveNode missing: %v", err)
	}
}

func TestBatchSpecificationExample(t *testing.T) {
	m := NewMaintainer(10, 20)
	mustAddNodes(t, m, 5)
	got, err := m.AddEdges([][2]int{{1, 2}, {3, 1}, {1, 0}})
	if err != nil {
		t.Fatal(err)
	}
	want := AddEdgesResult{
		Moved:  []int{3, 0, 2},
		Counts: []EdgeCount{{0, 0}, {2, 1}, {1, 2}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("AddEdges = %+v, want %+v", got, want)
	}
	assertOrder(t, m, []int{3, 1, 0, 2, 4})
	if m.Touched() != 6 {
		t.Fatalf("Touched = %d, want 6", m.Touched())
	}
}

func TestBatchRollback(t *testing.T) {
	m := NewMaintainer(10, 20)
	mustAddNodes(t, m, 5)
	_, err := m.AddEdges([][2]int{{1, 2}, {2, 1}})
	var batch *BatchError
	if !errors.As(err, &batch) || batch.Index != 1 {
		t.Fatalf("batch error = %#v, want index 1", err)
	}
	cycleWitness(t, batch.Reason, []int{1, 2})
	assertOrder(t, m, []int{0, 1, 2, 3, 4})
	if m.Touched() != 0 {
		t.Fatalf("Touched = %d, want 0", m.Touched())
	}
	if _, err := m.AddEdge(1, 2); err != nil {
		t.Fatalf("rolled-back first edge still present: %v", err)
	}
}

func TestBatchValidationAndDependencies(t *testing.T) {
	if _, err := NewMaintainer(10, 10).AddEdges(nil); !errors.Is(err, ErrInvalidBatchSize) {
		t.Fatalf("empty batch: %v", err)
	}

	m := NewMaintainer(2, 1)
	mustAddNodes(t, m, 2)
	_, err := m.AddEdges([][2]int{{0, 1}, {1, 0}})
	var batch *BatchError
	if !errors.As(err, &batch) || batch.Index != 1 || !errors.Is(batch.Reason, ErrEdgeLimit) {
		t.Fatalf("limit dependency error = %#v", err)
	}
	assertOrder(t, m, []int{0, 1})
}
