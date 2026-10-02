package twosizelru_test

import (
	"testing"

	"ontology/twosizelru"
)

func TestPromotionBoundary(t *testing.T) {
	manager := newManager(t, 8, 50, 0, 10)
	mustAccess(t, manager, 0, 0)
	mustAccess(t, manager, 1, 1)
	assertLists(t, manager, []int{0}, []int{1})

	mustAccess(t, manager, 1, 10)
	assertLists(t, manager, []int{0}, []int{1})

	mustAccess(t, manager, 1, 11)
	assertLists(t, manager, []int{1}, []int{0})
}

func TestPromotionImmediatelyWhenStayZero(t *testing.T) {
	manager := newManager(t, 8, 50, 0, 0)
	mustAccess(t, manager, 0, 0)
	mustAccess(t, manager, 1, 1)
	assertLists(t, manager, []int{0}, []int{1})

	mustAccess(t, manager, 1, 1)
	assertLists(t, manager, []int{1}, []int{0})
}

func TestPrefetchFirstHitRecordsOnly(t *testing.T) {
	manager := newManager(t, 8, 50, 2, 10)
	if _, err := manager.Prefetch(0, 0); err != nil {
		t.Fatalf("Prefetch(A): %v", err)
	}
	if _, err := manager.Prefetch(1, 1); err != nil {
		t.Fatalf("Prefetch(B): %v", err)
	}
	assertLists(t, manager, nil, []int{1, 0})

	mustAccess(t, manager, 1, 2)
	assertLists(t, manager, nil, []int{1, 0})

	mustAccess(t, manager, 1, 11)
	assertLists(t, manager, nil, []int{1, 0})

	mustAccess(t, manager, 1, 12)
	assertLists(t, manager, []int{1}, []int{0})
}

func TestPrefetchFirstHitDoesNotPromoteWhenStayZero(t *testing.T) {
	manager := newManager(t, 8, 90, 2, 0)
	mustPrefetch(t, manager, 0, 0)
	mustPrefetch(t, manager, 1, 1)
	assertLists(t, manager, nil, []int{1, 0})

	mustAccess(t, manager, 1, 1)
	assertLists(t, manager, nil, []int{1, 0})

	mustAccess(t, manager, 1, 1)
	assertLists(t, manager, []int{1}, []int{0})
}

func TestToleranceKeepsNewPageInOld(t *testing.T) {
	manager := newManager(t, 8, 50, 2, 10)
	mustAccess(t, manager, 0, 0)
	assertLists(t, manager, nil, []int{0})
}

func TestZeroToleranceMovesNewPageToYoungTail(t *testing.T) {
	manager := newManager(t, 8, 50, 0, 10)
	mustAccess(t, manager, 0, 0)
	assertLists(t, manager, []int{0}, nil)
}

func TestNewAccessFirstStartsAtCurrentNow(t *testing.T) {
	manager := newManager(t, 8, 70, 1, 10)
	mustAccess(t, manager, 0, 0)
	mustAccess(t, manager, 1, 1)

	mustAccess(t, manager, 2, 100)
	assertLists(t, manager, nil, []int{2, 1, 0})

	mustAccess(t, manager, 2, 109)
	assertLists(t, manager, nil, []int{2, 1, 0})

	mustAccess(t, manager, 2, 110)
	assertLists(t, manager, []int{2}, []int{1, 0})
}

func TestNowUpperBoundAccepted(t *testing.T) {
	manager := newManager(t, 8, 50, 0, 10)
	mustAccess(t, manager, 0, 1_000_000_000_000_000)
}

func TestYoungHitDoesNotSetFirst(t *testing.T) {
	manager := newManager(t, 8, 50, 0, 10)
	mustPrefetch(t, manager, 0, 0)
	mustPrefetch(t, manager, 1, 1)
	mustPrefetch(t, manager, 2, 2)
	assertLists(t, manager, []int{0, 2}, []int{1})

	mustAccess(t, manager, 2, 3)
	assertLists(t, manager, []int{2, 0}, []int{1})
	mustAccess(t, manager, 1, 5)
	mustAccess(t, manager, 1, 15)
	assertLists(t, manager, []int{1, 2}, []int{0})

	mustAccess(t, manager, 0, 15)
	assertLists(t, manager, []int{1, 2}, []int{0})
}

func newManager(t *testing.T, capacity int, oldPercent int, tolerance int, stay int64) *twosizelru.Manager {
	t.Helper()

	manager, err := twosizelru.New(capacity, oldPercent, tolerance, stay)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	return manager
}

func mustAccess(t *testing.T, manager *twosizelru.Manager, page int, now int64) twosizelru.Result {
	t.Helper()

	result, err := manager.Access(page, now)
	if err != nil {
		t.Fatalf("Access(%d, %d): %v", page, now, err)
	}
	return result
}

func mustPrefetch(t *testing.T, manager *twosizelru.Manager, page int, now int64) twosizelru.Result {
	t.Helper()

	result, err := manager.Prefetch(page, now)
	if err != nil {
		t.Fatalf("Prefetch(%d, %d): %v", page, now, err)
	}
	return result
}
