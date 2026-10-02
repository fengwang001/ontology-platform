package ontology

import (
	"errors"
	"slices"
	"sync"
	"testing"
)

func rejectKey(t *testing.T, err error, want error, position int, time int64) {
	t.Helper()
	var rejected rejectionError
	if !errors.As(err, &rejected) || !errors.Is(err, want) {
		t.Fatalf("err = %v, want %v", err, want)
	}
	if rejected.position != position || rejected.time != time {
		t.Fatalf("rejected key = (%d,%d), want (%d,%d)", rejected.position, rejected.time, position, time)
	}
}

func updateMust(t *testing.T, tracker *Tracker, batch []EntryDelta) []FrontierChange {
	t.Helper()
	changes, err := tracker.Update(batch)
	if err != nil {
		t.Fatalf("Update(%v): %v", batch, err)
	}
	return changes
}

func frontiersOf(tracker *Tracker) []int64 {
	_, frontiers := tracker.Frontiers()
	return frontiers
}

func TestExampleBatchesAndPathSummary(t *testing.T) {
	tracker, err := NewTracker(4, []Edge{{0, 1, 0}, {1, 2, 1}, {2, 1, 1}, {2, 3, 0}}, []int{0})
	if err != nil {
		t.Fatal(err)
	}
	if tracker.dist[0][2] != 1 || tracker.dist[2][1] != 1 || tracker.dist[1][1] != 0 {
		t.Fatalf("distance summary = %v", tracker.dist)
	}

	changes := updateMust(t, tracker, []EntryDelta{{0, 5, 1}})
	wantChanges := []FrontierChange{{0, -1, 5}, {1, -1, 5}, {2, -1, 6}, {3, -1, 6}}
	if !slices.Equal(changes, wantChanges) {
		t.Fatalf("changes = %v, want %v", changes, wantChanges)
	}

	changes = updateMust(t, tracker, []EntryDelta{{0, 5, -1}, {1, 5, 1}})
	if !slices.Equal(changes, []FrontierChange{{0, 5, -1}}) {
		t.Fatalf("consume/produce changes = %v", changes)
	}

	changes = updateMust(t, tracker, []EntryDelta{{1, 5, -1}, {2, 6, 1}})
	if !slices.Equal(changes, []FrontierChange{{1, 5, 7}}) {
		t.Fatalf("move changes = %v", changes)
	}
	if !slices.Equal(frontiersOf(tracker), []int64{-1, 7, 6, 6}) {
		t.Fatalf("frontiers = %v", frontiersOf(tracker))
	}

	_, err = tracker.Update([]EntryDelta{{3, 5, 1}})
	rejectKey(t, err, ErrCausalityViolation, 3, 5)
	if ver, frontiers := tracker.Frontiers(); ver != 3 || !slices.Equal(frontiers, []int64{-1, 7, 6, 6}) {
		t.Fatalf("after rejection = (%d,%v)", ver, frontiers)
	}

	for _, tc := range []struct {
		position int
		time     int64
		want     bool
	}{{1, 6, true}, {1, 7, false}, {0, maxTime, true}} {
		got, err := tracker.Complete(tc.position, tc.time)
		if err != nil || got != tc.want {
			t.Fatalf("Complete(%d,%d) = (%v,%v), want %v", tc.position, tc.time, got, err, tc.want)
		}
	}
}

func TestSelfHeldEntryAndCycles(t *testing.T) {
	tracker, err := NewTracker(2, []Edge{{1, 1, 1}, {0, 1, 2}}, []int{1})
	if err != nil {
		t.Fatal(err)
	}
	updateMust(t, tracker, []EntryDelta{{1, 4, 1}})
	if got := frontiersOf(tracker); !slices.Equal(got, []int64{-1, 4}) {
		t.Fatalf("frontiers = %v", got)
	}

	for _, edges := range [][]Edge{{{0, 0, 0}}, {{0, 1, 0}, {1, 0, 0}}, {{0, 1, 0}, {1, 2, 0}, {2, 0, 0}}} {
		if _, err := NewTracker(2, edges, []int{0}); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("zero cycle edges %v: err = %v", edges, err)
		}
	}

	positive, err := NewTracker(2, []Edge{{0, 1, 0}, {1, 0, 1}}, []int{0})
	if err != nil {
		t.Fatal(err)
	}
	if positive.dist[1][1] != 0 {
		t.Fatalf("positive cycle diagonal = %d", positive.dist[1][1])
	}
}

func TestCausalityAggregationAndRejections(t *testing.T) {
	tracker, err := NewTracker(1, nil, []int{0})
	if err != nil {
		t.Fatal(err)
	}
	updateMust(t, tracker, []EntryDelta{{0, 5, 1}})
	updateMust(t, tracker, []EntryDelta{{0, 5, -2}, {0, 5, 1}})
	_, err = tracker.Update([]EntryDelta{{0, 6, 1}, {0, 6, -2}})
	rejectKey(t, err, ErrNegativeCount, 0, 6)
	if tracker.version != 2 {
		t.Fatalf("version after rejection = %d", tracker.version)
	}

	nonSource, err := NewTracker(2, nil, []int{0})
	if err != nil {
		t.Fatal(err)
	}
	_, err = nonSource.Update([]EntryDelta{{1, 2, -1}, {1, 3, 1}})
	rejectKey(t, err, ErrNegativeCount, 1, 2)
	_, err = nonSource.Update([]EntryDelta{{1, 2, 1}})
	rejectKey(t, err, ErrCausalityViolation, 1, 2)

	if _, err := tracker.Update(nil); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("empty batch err = %v", err)
	}
}

func TestCausalityExactlyAtFrontier(t *testing.T) {
	tracker, err := NewTracker(2, []Edge{{0, 1, 3}}, []int{0})
	if err != nil {
		t.Fatal(err)
	}
	updateMust(t, tracker, []EntryDelta{{0, 4, 1}})
	if _, err := tracker.Update([]EntryDelta{{1, 6, 1}}); !errors.Is(err, ErrCausalityViolation) {
		t.Fatalf("t=F-1 err=%v", err)
	}
	updateMust(t, tracker, []EntryDelta{{1, 7, 1}})
	updateMust(t, tracker, []EntryDelta{{0, 4, -1}, {1, 7, 1}})
}

func TestCountCapPrecedesNegativeKey(t *testing.T) {
	tracker, err := NewTracker(2, nil, []int{0})
	if err != nil {
		t.Fatal(err)
	}
	tracker.counts[entryKey{position: 0, time: 1}] = maxCount
	_, err = tracker.Update([]EntryDelta{{0, 1, 1}, {1, 2, -1}})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("cap priority err=%v", err)
	}
}

func TestEntryVisitsAndConcurrency(t *testing.T) {
	visits := func(active int) int64 {
		tracker, err := NewTracker(2, []Edge{{0, 1, 1}}, []int{0})
		if err != nil {
			t.Fatal(err)
		}
		for time := range active {
			updateMust(t, tracker, []EntryDelta{{0, int64(time), 1}})
		}
		before := tracker.entryVisits
		updateMust(t, tracker, []EntryDelta{{0, int64(active + 10), 1}})
		increment := tracker.entryVisits - before
		if increment > 2 {
			t.Fatalf("increment = %d", increment)
		}
		return increment
	}
	if visits(10) != visits(10000) {
		t.Fatal("entry-visit increments differ")
	}

	tracker, err := NewTracker(2, []Edge{{0, 1, 1}}, []int{0})
	if err != nil {
		t.Fatal(err)
	}
	var wait sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		wait.Add(1)
		go func(id int) {
			defer wait.Done()
			for i := 0; i < 100; i++ {
				time := int64(id*100 + i)
				_, _ = tracker.Update([]EntryDelta{{0, time, 1}})
				_ = frontiersOf(tracker)
				_, _ = tracker.Complete(0, time)
			}
		}(worker)
	}
	wait.Wait()
	if ver, frontiers := tracker.Frontiers(); ver != 800 || frontiers[0] != 0 || frontiers[1] != 1 {
		t.Fatalf("ver=%d frontiers=%v", ver, frontiers)
	}
}
