package ontology

import "testing"

func TestSameBatchSequenceReplaysIdentically(t *testing.T) {
	edges := []Edge{{0, 1, 0}, {1, 2, 1}, {2, 1, 1}, {2, 3, 0}}
	sources := []int{0}
	first, err := NewTracker(4, edges, sources)
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewTracker(4, append([]Edge(nil), edges...), append([]int(nil), sources...))
	if err != nil {
		t.Fatal(err)
	}

	batches := [][]Delta{
		{{0, 5, 1}},
		{{0, 5, -1}, {1, 5, 1}},
		{{1, 5, -1}, {2, 6, 1}},
		{{3, 5, 1}},
	}

	for _, batch := range batches {
		firstVersion, firstChanges, firstRejection := first.Update(batch)
		secondVersion, secondChanges, secondRejection := second.Update(batch)
		if firstVersion != secondVersion ||
			!equalChanges(firstChanges, secondChanges) ||
			!sameRejection(firstRejection, secondRejection) {
			t.Fatalf("replay output differs for %v", batch)
		}
	}

	firstVersion, firstFrontiers := first.Frontiers()
	secondVersion, secondFrontiers := second.Frontiers()
	if firstVersion != secondVersion ||
		!equalInt64Slices(firstFrontiers, secondFrontiers) ||
		!equalLedgers(first.snapshotLedger(), second.snapshotLedger()) {
		t.Fatalf("replayed final state differs")
	}
}

func equalChanges(left, right []FrontierChange) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func equalInt64Slices(left, right []int64) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func equalLedgers(left, right map[ledgerKey]uint64) bool {
	if len(left) != len(right) {
		return false
	}
	for key, count := range left {
		if right[key] != count {
			return false
		}
	}
	return true
}
