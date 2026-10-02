package twosizelru_test

import (
	"errors"
	"testing"

	"ontology/twosizelru"
)

func TestExampleSequence(t *testing.T) {
	manager, err := twosizelru.New(8, 50, 0, 10)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	accesses := []struct {
		page int
		now  int64
	}{
		{0, 0},
		{1, 1},
		{2, 2},
		{3, 3},
	}
	for _, access := range accesses {
		if _, err := manager.Access(access.page, access.now); err != nil {
			t.Fatalf("Access(%d, %d) returned error: %v", access.page, access.now, err)
		}
	}

	assertLists(t, manager, []int{0, 2}, []int{3, 1})

	if _, err := manager.Access(1, 5); err != nil {
		t.Fatalf("Access(B, 5) returned error: %v", err)
	}
	assertLists(t, manager, []int{0, 2}, []int{3, 1})

	if _, err := manager.Access(1, 11); err != nil {
		t.Fatalf("Access(B, 11) returned error: %v", err)
	}
	assertLists(t, manager, []int{1, 0}, []int{2, 3})

	result, err := manager.Prefetch(4, 12)
	if err != nil {
		t.Fatalf("Prefetch(E, 12) returned error: %v", err)
	}
	if result.Hit || !result.Read || result.Evicted {
		t.Fatalf("Prefetch(E, 12) returned %+v", result)
	}
	assertLists(t, manager, []int{1, 0, 4}, []int{2, 3})
}

func assertLists(t *testing.T, manager *twosizelru.Manager, wantYoung []int, wantOld []int) {
	t.Helper()

	young, old := manager.Lists()
	if !equalSlices(young, wantYoung) || !equalSlices(old, wantOld) {
		t.Fatalf("Lists() = Y%v O%v, want Y%v O%v", young, old, wantYoung, wantOld)
	}
}

func equalSlices(actual []int, want []int) bool {
	if len(actual) != len(want) {
		return false
	}
	for i := range actual {
		if actual[i] != want[i] {
			return false
		}
	}
	return true
}

func assertReject(t *testing.T, err error, want twosizelru.RejectReason) {
	t.Helper()

	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
}
