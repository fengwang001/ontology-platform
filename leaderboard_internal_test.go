package ontology

import (
	"errors"
	"testing"
)

func TestSpecScenario(t *testing.T) {
	lb, err := NewLeaderboard(10, 3, 100, 3, 1)
	if err != nil {
		t.Fatal(err)
	}

	add := func(id string, d, at int64) {
		t.Helper()
		if err := lb.Add(id, d, at); err != nil {
			t.Fatalf("Add(%q,%d,%d): %v", id, d, at, err)
		}
	}
	snapshot := func(now int64, wantItems []RankedItem, wantDropped []DroppedItem) {
		t.Helper()
		got, err := lb.Snapshot(now)
		if err != nil {
			t.Fatalf("Snapshot(%d): %v", now, err)
		}
		t.Logf("Snapshot(%d) => items=%+v dropped=%+v prev=%+v", now, got.Items, got.Dropped, lb.prev)
		assertResult(t, got, wantItems, wantDropped)
	}

	add("a", 60, 0)
	add("b", 100, 5)
	add("a", 50, 12)
	snapshot(15,
		[]RankedItem{
			{ID: "a", Score: 110, Rank: 1, New: true},
			{ID: "b", Score: 100, Rank: 2, New: true},
		},
		nil)

	add("c", 100, 25)
	snapshot(25,
		[]RankedItem{
			{ID: "a", Score: 110, Rank: 1, Change: 0},
			{ID: "b", Score: 100, Rank: 2, Change: 0},
			{ID: "c", Score: 100, Rank: 2, New: true},
		},
		nil)

	snapshot(35,
		[]RankedItem{
			{ID: "c", Score: 100, Rank: 1, Change: 1},
		},
		[]DroppedItem{{ID: "a", PrevRank: 1}, {ID: "b", PrevRank: 2}})

	add("a", 30, 36)
	snapshot(36,
		[]RankedItem{
			{ID: "c", Score: 100, Rank: 1, Change: 0},
		},
		nil)

	add("c", 80, 52)
	snapshot(52,
		[]RankedItem{
			{ID: "c", Score: 80, Rank: 1, Change: 0},
		},
		nil)

	snapshot(53, nil, []DroppedItem{{ID: "c", PrevRank: 1}})
}

func TestRejectPriorityLeavesStateUnchanged(t *testing.T) {
	lb, _ := NewLeaderboard(10, 3, 100, 3, 1)
	if err := lb.Add("", 100, 0); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("invalid id: %v", err)
	}
	if err := lb.Add("a", 100, 40); err != nil {
		t.Fatal(err)
	}
	if err := lb.Add("a", 1, -30); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("invalid time before expired: %v", err)
	}
	if err := lb.Add("a", 1, -30); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("invalid time before clock rollback: %v", err)
	}
	if _, err := lb.Snapshot(-1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("invalid now before clock rollback: %v", err)
	}
	if err := lb.Add("late", 100, 10); !errors.Is(err, ErrExpired) {
		t.Fatalf("bucket cur-W: %v", err)
	}
	if _, err := lb.Snapshot(5); !errors.Is(err, ErrClockRolledBack) {
		t.Fatalf("clock rollback: %v", err)
	}
	if got := lb.Score("a"); got != 100 {
		t.Fatalf("score after rejected ops = %d", got)
	}
}

func assertResult(t *testing.T, got SnapshotResult, wantItems []RankedItem, wantDropped []DroppedItem) {
	t.Helper()
	if len(got.Items) != len(wantItems) {
		t.Fatalf("items len = %d, want %d (%+v)", len(got.Items), len(wantItems), got.Items)
	}
	for i := range wantItems {
		if got.Items[i] != wantItems[i] {
			t.Fatalf("item %d = %+v, want %+v", i, got.Items[i], wantItems[i])
		}
	}
	if len(got.Dropped) != len(wantDropped) {
		t.Fatalf("dropped len = %d, want %d (%+v)", len(got.Dropped), len(wantDropped), got.Dropped)
	}
	for i := range wantDropped {
		if got.Dropped[i] != wantDropped[i] {
			t.Fatalf("dropped %d = %+v, want %+v", i, got.Dropped[i], wantDropped[i])
		}
	}
}
