package ontology

import (
	"errors"
	"reflect"
	"testing"
)

func addForTest(t *testing.T, m *IncrementalBridges, u, v int, w int64, kind AddKind) AddResult {
	t.Helper()
	result, err := m.AddEdge(u, v, w)
	if err != nil {
		t.Fatalf("AddEdge(%d,%d,%d): %v", u, v, w, err)
	}
	if result.Kind != kind {
		t.Fatalf("AddEdge(%d,%d,%d) kind = %s, want %s", u, v, w, result.Kind, kind)
	}
	return result
}

func TestSpecExample(t *testing.T) {
	m, err := NewIncrementalBridges(6, 20)
	if err != nil {
		t.Fatal(err)
	}
	weights := []int64{5, 3, 4, 2, 6, 7, 1, 8, 2}
	edges := [][2]int{{0, 1}, {1, 2}, {2, 3}, {4, 5}, {4, 5}, {0, 3}, {3, 4}, {1, 5}, {2, 3}}
	for i := 0; i < 4; i++ {
		result := addForTest(t, m, edges[i][0], edges[i][1], weights[i], KindLink)
		if result.ID != i+1 || result.Version != i+1 {
			t.Fatalf("link result = %+v", result)
		}
	}

	min, err := m.MinBridge(0, 3)
	if err != nil || min != (BridgeWeight{ID: 2, Weight: 3}) {
		t.Fatalf("MinBridge before merge = %+v, %v", min, err)
	}

	r5 := addForTest(t, m, 4, 5, weights[4], KindMerge)
	if !reflect.DeepEqual(r5.Merged, []int{4, 5}) || r5.NewLabel != 4 ||
		!reflect.DeepEqual(r5.Unbridged, []int{4}) || r5.NewWeight != 8 {
		t.Fatalf("parallel merge = %+v", r5)
	}
	block, _ := m.Block(5)
	if block != 4 {
		t.Fatalf("Block(5) = %d", block)
	}

	r6 := addForTest(t, m, 0, 3, weights[5], KindMerge)
	if !reflect.DeepEqual(r6.Merged, []int{0, 1, 2, 3}) || r6.NewLabel != 0 ||
		!reflect.DeepEqual(r6.Unbridged, []int{1, 2, 3}) || r6.NewWeight != 19 {
		t.Fatalf("chain closure merge = %+v", r6)
	}
	if m.BridgeCount() != 0 || m.BlockCount() != 2 || m.ComponentCount() != 2 {
		t.Fatalf("counts after r6 = bridges %d blocks %d comps %d", m.BridgeCount(), m.BlockCount(), m.ComponentCount())
	}
	if _, err := m.BridgesOnPath(0, 4); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("disconnected error = %v", err)
	}

	r7 := addForTest(t, m, 3, 4, weights[6], KindLink)
	if r7.ID != 7 || !mustBridge(t, m, 7) {
		t.Fatalf("link edge = %+v", r7)
	}
	path, err := m.BridgesOnPath(0, 5)
	if err != nil || !reflect.DeepEqual(path, []int{7}) {
		t.Fatalf("BridgesOnPath = %v, %v", path, err)
	}
	path, err = m.BridgesOnPath(4, 5)
	if err != nil || len(path) != 0 {
		t.Fatalf("same block path = %v, %v", path, err)
	}
	min, err = m.MinBridge(0, 5)
	if err != nil || min != (BridgeWeight{ID: 7, Weight: 1}) {
		t.Fatalf("MinBridge bridge 7 = %+v, %v", min, err)
	}

	r8 := addForTest(t, m, 1, 5, weights[7], KindMerge)
	if !reflect.DeepEqual(r8.Merged, []int{0, 4}) || r8.NewLabel != 0 ||
		!reflect.DeepEqual(r8.Unbridged, []int{7}) || r8.NewWeight != 36 {
		t.Fatalf("final merge = %+v", r8)
	}

	r9 := addForTest(t, m, 2, 3, weights[8], KindInside)
	if r9.NewWeight != 0 {
		t.Fatalf("inside NewWeight = %d", r9.NewWeight)
	}
	weight, _ := m.BlockWeight(0)
	if weight != 38 || m.BridgeCount() != 0 || m.BlockCount() != 1 || m.ComponentCount() != 1 {
		t.Fatalf("final state weight %d bridges %d blocks %d comps %d", weight, m.BridgeCount(), m.BlockCount(), m.ComponentCount())
	}

	history := map[int][2]int{1: {1, 6}, 4: {4, 5}, 5: {5, 5}, 7: {7, 8}, 9: {9, 9}}
	for id, want := range history {
		born, unbridged, err := m.EdgeHistory(id)
		if err != nil || born != want[0] || unbridged != want[1] {
			t.Fatalf("history(%d) = (%d,%d),%v want %v", id, born, unbridged, err, want)
		}
	}
}

func mustBridge(t *testing.T, m *IncrementalBridges, id int) bool {
	t.Helper()
	got, err := m.IsBridge(id)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestRejectionDoesNotConsumeIDOrVersion(t *testing.T) {
	m, _ := NewIncrementalBridges(2, 1)
	result, err := m.AddEdge(0, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	before := result.Version
	if _, err := m.AddEdge(2, 1, 1); !errors.Is(err, ErrInvalidNode) {
		t.Fatalf("node error = %v", err)
	}
	if _, err := m.AddEdge(0, 0, 1); !errors.Is(err, ErrSelfLoop) {
		t.Fatalf("self loop error = %v", err)
	}
	if _, err := m.AddEdge(0, 1, 0); !errors.Is(err, ErrInvalidWeight) {
		t.Fatalf("weight error = %v", err)
	}
	if _, err := m.AddEdge(0, 1, 2); !errors.Is(err, ErrEdgeLimit) {
		t.Fatalf("limit error = %v", err)
	}
	stillBridge, err := m.IsBridge(1)
	if err != nil || !stillBridge || m.Steps() != 0 || result.Version != before {
		t.Fatalf("state changed after rejection: bridge %v version-related result %+v steps %d", stillBridge, result, m.Steps())
	}
	if _, err := m.IsBridge(2); !errors.Is(err, ErrEdgeNotFound) {
		t.Fatalf("missing edge error = %v", err)
	}
}
