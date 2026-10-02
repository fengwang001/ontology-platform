package ontology

import "testing"

func TestEntryVisitsIndependentOfActiveTimestampCount(t *testing.T) {
	counts := []int{10, 10_000}
	increments := make([]uint64, len(counts))

	for i, activeCount := range counts {
		tracker, err := NewTracker(1, nil, []int{0})
		if err != nil {
			t.Fatal(err)
		}

		for time := int64(1); time <= int64(activeCount); time++ {
			key := ledgerKey{position: 0, time: time}
			tracker.ledger[key] = 1
			tracker.active.add(0, time)
		}
		tracker.frontiers[0] = 1

		before := tracker.EntryVisits()
		mustUpdate(t, tracker, []Delta{{0, 0, 1}})
		increments[i] = tracker.EntryVisits() - before

		if increments[i] > 2 {
			t.Fatalf("entry visit increment = %d, want <= 2", increments[i])
		}
	}

	if increments[0] != increments[1] {
		t.Fatalf("increments differ at 10 vs 10000 active timestamps: %d vs %d", increments[0], increments[1])
	}
}
