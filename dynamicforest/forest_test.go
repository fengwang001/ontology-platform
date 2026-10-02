package dynamicforest

import (
	"errors"
	"reflect"
	"testing"
)

func requireResult(t *testing.T, result *UpdateResult, want *UpdateResult) {
	t.Helper()
	if result.Version != want.Version || result.Weight != want.Weight || result.Components != want.Components ||
		!reflect.DeepEqual(result.Entered, want.Entered) || !reflect.DeepEqual(result.Left, want.Left) {
		t.Fatalf("unexpected result: got %+v want %+v", result, want)
	}
}

func TestExampleSequence(t *testing.T) {
	service, err := NewService(5, 20)
	if err != nil {
		t.Fatal(err)
	}

	add := func(u, v int, weight int64, want *UpdateResult) {
		t.Helper()
		_, result, err := service.AddEdge(u, v, weight)
		if err != nil {
			t.Fatal(err)
		}
		requireResult(t, result, want)
	}

	add(0, 1, 4, &UpdateResult{1, []int{1}, []int{}, 4, 4})
	add(1, 2, 6, &UpdateResult{2, []int{2}, []int{}, 10, 3})
	add(0, 2, 6, &UpdateResult{3, []int{}, []int{}, 10, 3})
	add(0, 2, 5, &UpdateResult{4, []int{4}, []int{2}, 9, 3})

	result, err := service.SetWeight(3, 5)
	if err != nil {
		t.Fatal(err)
	}
	requireResult(t, result, &UpdateResult{5, []int{3}, []int{4}, 9, 3})
	since, err := service.TreeSince(3)
	if err != nil || since != 5 {
		t.Fatalf("TreeSince(3)=%d,%v", since, err)
	}

	result, err = service.SetWeight(1, 10)
	if err != nil {
		t.Fatal(err)
	}
	requireResult(t, result, &UpdateResult{6, []int{2}, []int{1}, 11, 3})

	result, err = service.RemoveEdge(2)
	if err != nil {
		t.Fatal(err)
	}
	requireResult(t, result, &UpdateResult{7, []int{1}, []int{2}, 15, 3})

	result, err = service.RemoveEdge(1)
	if err != nil {
		t.Fatal(err)
	}
	requireResult(t, result, &UpdateResult{8, []int{}, []int{1}, 5, 4})

	result, err = service.SetWeight(3, 2)
	if err != nil {
		t.Fatal(err)
	}
	requireResult(t, result, &UpdateResult{9, []int{}, []int{}, 2, 4})
	since, err = service.TreeSince(3)
	if err != nil || since != 5 {
		t.Fatalf("TreeSince(3)=%d,%v after decrease", since, err)
	}

	result, err = service.SetWeight(3, 100)
	if err != nil {
		t.Fatal(err)
	}
	requireResult(t, result, &UpdateResult{10, []int{4}, []int{3}, 5, 4})
	since, _ = service.TreeSince(4)
	if since != 10 {
		t.Fatalf("TreeSince(4)=%d", since)
	}
	since, _ = service.TreeSince(3)
	if since != 0 {
		t.Fatalf("TreeSince(3)=%d", since)
	}
}

func TestPathMaxTieUsesLargerID(t *testing.T) {
	service, err := NewService(4, 10)
	if err != nil {
		t.Fatal(err)
	}
	service.AddEdge(0, 1, 7)
	service.AddEdge(1, 2, 7)

	path, err := service.PathMax(0, 2)
	if err != nil || path.EdgeID != 2 || path.Weight != 7 {
		t.Fatalf("PathMax=%+v,%v", path, err)
	}

	if _, err := service.PathMax(1, 1); !errors.Is(err, ErrSameNode) {
		t.Fatalf("same node error=%v", err)
	}
	if _, err := service.PathMax(0, 3); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("connected same component unexpectedly: %v", err)
	}
}

func TestRejectedOperationsDoNotConsumeIDOrVersion(t *testing.T) {
	service, _ := NewService(2, 1)
	if _, _, err := service.AddEdge(0, 0, 1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("self edge error=%v", err)
	}
	if _, _, err := service.AddEdge(0, 1, maxWeight+1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("weight error=%v", err)
	}

	id, _, err := service.AddEdge(0, 1, 1)
	if err != nil || id != 1 {
		t.Fatalf("accepted id=%d err=%v", id, err)
	}
	if _, _, err := service.AddEdge(0, 1, 1); !errors.Is(err, ErrCapacityFull) {
		t.Fatalf("capacity error=%v", err)
	}
	if _, err := service.SetWeight(2, 1); !errors.Is(err, ErrEdgeNotFound) {
		t.Fatalf("set missing error=%v", err)
	}
	if _, err := service.RemoveEdge(2); !errors.Is(err, ErrEdgeNotFound) {
		t.Fatalf("remove missing error=%v", err)
	}
	if version := service.Version(); version != 1 {
		t.Fatalf("version=%d", version)
	}
}

func TestNegativeWeightsAndDeletionWithoutReplacement(t *testing.T) {
	service, _ := NewService(3, 10)
	id1, result, _ := service.AddEdge(0, 1, -10)
	if id1 != 1 || result.Weight != -10 || result.Components != 2 {
		t.Fatalf("negative add result id=%d result=%+v", id1, result)
	}
	id2, _, _ := service.AddEdge(1, 2, -5)
	if id2 != 2 {
		t.Fatalf("id2=%d", id2)
	}
	result, err := service.RemoveEdge(1)
	if err != nil || result.Weight != -5 || result.Components != 2 || !reflect.DeepEqual(result.Left, []int{1}) || len(result.Entered) != 0 {
		t.Fatalf("remove result=%+v err=%v", result, err)
	}
}

func TestScannedSmallSideOfLongChain(t *testing.T) {
	const n = 100_000
	service, err := NewService(n, n+10)
	if err != nil {
		t.Fatal(err)
	}
	for node := 1; node < n; node++ {
		if _, _, err := service.AddEdge(node-1, node, int64(node)); err != nil {
			t.Fatal(err)
		}
	}

	before := service.Scanned()
	result, err := service.RemoveEdge(1)
	if err != nil {
		t.Fatal(err)
	}
	delta := service.Scanned() - before
	if delta > 4 {
		t.Fatalf("scanned delta=%d for singleton side, want <= 4", delta)
	}
	if result.Components != 2 || len(result.Entered) != 0 || !reflect.DeepEqual(result.Left, []int{1}) {
		t.Fatalf("unexpected leaf-edge removal: %+v", result)
	}
}
