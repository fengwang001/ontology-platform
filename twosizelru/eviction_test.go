package twosizelru_test

import (
	"testing"

	"ontology/twosizelru"
)

type snapshot struct {
	young []int
	old   []int
}

func TestEvictionSelectsOldTailThenYoungTail(t *testing.T) {
	manager := newManager(t, 4, 50, 0, 100)
	for page, now := 0, int64(0); page < 4; page, now = page+1, now+1 {
		mustAccess(t, manager, page, now)
	}
	assertLists(t, manager, []int{0, 2}, []int{3, 1})

	if err := manager.Pin(1); err != nil {
		t.Fatalf("Pin(1): %v", err)
	}
	result := mustAccess(t, manager, 4, 4)
	if !result.Evicted || result.EvictedPage != 3 {
		t.Fatalf("Access(E) = %+v, want eviction of page 3", result)
	}
	assertLists(t, manager, []int{0, 2}, []int{4, 1})

	if err := manager.Pin(1); err != nil {
		t.Fatalf("Pin(1): %v", err)
	}
	if err := manager.Pin(4); err != nil {
		t.Fatalf("Pin(4): %v", err)
	}
	result = mustAccess(t, manager, 5, 5)
	if !result.Evicted || result.EvictedPage != 2 {
		t.Fatalf("Access(F) = %+v, want eviction of page 2", result)
	}
	assertLists(t, manager, []int{0, 5}, []int{4, 1})
}

func TestAllPinnedRejectsAndPreservesClockAndState(t *testing.T) {
	manager := newManager(t, 4, 50, 0, 100)
	for page, now := 0, int64(0); page < 4; page, now = page+1, now+1 {
		mustAccess(t, manager, page, now)
	}
	for page := 0; page < 4; page++ {
		if err := manager.Pin(page); err != nil {
			t.Fatalf("Pin(%d): %v", page, err)
		}
	}

	before := getSnapshot(t, manager)
	_, err := manager.Access(4, 4)
	assertReject(t, err, twosizelru.RejectAllPinned)
	assertSnapshot(t, manager, before)

	if _, err := manager.Prefetch(5, 5); !errorsIs(err, twosizelru.RejectAllPinned) {
		t.Fatalf("Prefetch after full pin error = %v, want all pinned", err)
	}
	assertSnapshot(t, manager, before)

	_, err = manager.Access(0, 2)
	assertReject(t, err, twosizelru.RejectClockRollback)
	assertSnapshot(t, manager, before)

	mustAccess(t, manager, 0, 4)
	young, old := manager.Lists()
	if len(young)+len(old) != 4 {
		t.Fatalf("accepted hit changed pool size to %d", len(young)+len(old))
	}
}

func TestRejectionsPreserveState(t *testing.T) {
	manager := newManager(t, 4, 50, 0, 10)
	mustAccess(t, manager, 0, 0)
	mustAccess(t, manager, 1, 1)
	before := getSnapshot(t, manager)

	_, err := manager.Access(2, -1)
	assertReject(t, err, twosizelru.RejectInvalidArgument)
	assertSnapshot(t, manager, before)

	_, err = manager.Access(2, 0)
	assertReject(t, err, twosizelru.RejectClockRollback)
	assertSnapshot(t, manager, before)

	err = manager.Pin(9)
	assertReject(t, err, twosizelru.RejectPageNotFound)
	assertSnapshot(t, manager, before)

	err = manager.Unpin(9)
	assertReject(t, err, twosizelru.RejectPageNotFound)
	assertSnapshot(t, manager, before)

	err = manager.Unpin(0)
	assertReject(t, err, twosizelru.RejectUnpinUnderflow)
	assertSnapshot(t, manager, before)

	_, err = manager.Prefetch(0, 1)
	if err != nil {
		t.Fatalf("prefetch existing page: %v", err)
	}
	assertSnapshot(t, manager, before)
}

func TestInvalidConstruction(t *testing.T) {
	cases := []struct {
		name       string
		capacity   int
		oldPercent int
		tolerance  int
		stay       int64
	}{
		{"capacity too small", 3, 50, 0, 0},
		{"capacity too large", 1_000_001, 50, 0, 0},
		{"percent too small", 4, 4, 0, 0},
		{"percent too large", 4, 96, 0, 0},
		{"tolerance negative", 4, 50, -1, 0},
		{"tolerance too large", 4, 50, 5, 0},
		{"stay negative", 4, 50, 0, -1},
		{"stay too large", 4, 50, 0, 1_000_000_001},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := twosizelru.New(tc.capacity, tc.oldPercent, tc.tolerance, tc.stay)
			assertReject(t, err, twosizelru.RejectInvalidArgument)
		})
	}
}

func getSnapshot(t *testing.T, manager *twosizelru.Manager) snapshot {
	t.Helper()

	young, old := manager.Lists()
	return snapshot{append([]int(nil), young...), append([]int(nil), old...)}
}

func assertSnapshot(t *testing.T, manager *twosizelru.Manager, want snapshot) {
	t.Helper()
	assertLists(t, manager, want.young, want.old)
}

func errorsIs(err error, target error) bool {
	return err != nil && err.Error() == target.Error()
}
