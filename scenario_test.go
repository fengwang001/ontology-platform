package ontology

import (
	"errors"
	"reflect"
	"testing"
)

func mustUpdate(t *testing.T, tracker *Tracker, batch []Delta) (uint64, []FrontierChange) {
	t.Helper()
	version, changes, rejection := tracker.Update(batch)
	if rejection.Reason != nil {
		t.Fatalf("Update(%v) rejected: %#v", batch, rejection)
	}
	return version, changes
}

func assertRejection(t *testing.T, tracker *Tracker, batch []Delta, reason error, position int, time int64) {
	t.Helper()
	versionBefore, _ := tracker.Frontiers()
	ledgerBefore := tracker.snapshotLedger()
	_, _, rejection := tracker.Update(batch)
	if !errors.Is(rejection.Reason, reason) || rejection.Position != position || rejection.Time != time {
		t.Fatalf("Update(%v) rejection = %#v, want reason %v at (%d,%d)", batch, rejection, reason, position, time)
	}
	versionAfter, _ := tracker.Frontiers()
	if versionAfter != versionBefore || !reflect.DeepEqual(tracker.snapshotLedger(), ledgerBefore) {
		t.Fatalf("rejected Update changed state: ver %d->%d ledger %v->%v", versionBefore, versionAfter, ledgerBefore, tracker.snapshotLedger())
	}
}

func (t *Tracker) snapshotLedger() map[ledgerKey]uint64 {
	t.mu.RLock()
	defer t.mu.RUnlock()
	snapshot := make(map[ledgerKey]uint64, len(t.ledger))
	for key, count := range t.ledger {
		snapshot[key] = count
	}
	return snapshot
}

func TestConfigurationValidation(t *testing.T) {
	validEdges := []Edge{{0, 1, 0}}
	validSources := []int{0}

	cases := []struct {
		name    string
		n       int
		edges   []Edge
		sources []int
	}{
		{"n too small", 0, validEdges, validSources},
		{"n too large", 65, validEdges, validSources},
		{"edge out of bounds", 2, []Edge{{0, 2, 0}}, validSources},
		{"negative delay", 2, []Edge{{0, 1, -1}}, validSources},
		{"delay too large", 2, []Edge{{0, 1, 1_000_001}}, validSources},
		{"too many edges", 2, make([]Edge, 513), validSources},
		{"empty sources", 2, validEdges, nil},
		{"duplicate sources", 2, validEdges, []int{0, 0}},
		{"source out of bounds", 2, validEdges, []int{2}},
		{"zero self loop", 2, []Edge{{1, 1, 0}}, validSources},
		{"zero directed cycle", 3, []Edge{{0, 1, 1}, {1, 2, 0}, {2, 1, 0}}, validSources},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewTracker(tc.n, tc.edges, tc.sources); !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("NewTracker() error = %v, want %v", err, ErrInvalidConfig)
			}
		})
	}
}

func TestLocalEntryAndOneDelayCycle(t *testing.T) {
	tracker, err := NewTracker(2, []Edge{{1, 1, 1}, {1, 0, 0}}, []int{1})
	if err != nil {
		t.Fatal(err)
	}

	_, changes := mustUpdate(t, tracker, []Delta{{1, 4, 1}})
	if !reflect.DeepEqual(changes, []FrontierChange{{0, -1, 4}, {1, -1, 4}}) {
		t.Fatalf("changes = %#v", changes)
	}

	frontier, err := tracker.Frontier(1)
	if err != nil || frontier != 4 {
		t.Fatalf("Frontier(1) = (%d,%v)", frontier, err)
	}
}

func TestCausalEqualityAndSourceExemption(t *testing.T) {
	tracker, err := NewTracker(2, []Edge{{0, 1, 3}}, []int{0})
	if err != nil {
		t.Fatal(err)
	}

	mustUpdate(t, tracker, []Delta{{0, 10, 1}})
	assertRejection(t, tracker, []Delta{{1, 12, 1}}, ErrCausalViolation, 1, 12)
	mustUpdate(t, tracker, []Delta{{1, 13, 1}})

	frontier, err := tracker.Frontier(1)
	if err != nil || frontier != 13 {
		t.Fatalf("Frontier(1) = (%d,%v)", frontier, err)
	}

	mustUpdate(t, tracker, []Delta{{0, 0, 1}})
}

func TestConsumeAndProduceUsesOldFrontier(t *testing.T) {
	tracker, err := NewTracker(3, []Edge{{0, 1, 1}, {1, 2, 1}}, []int{0})
	if err != nil {
		t.Fatal(err)
	}

	mustUpdate(t, tracker, []Delta{{0, 5, 1}})
	mustUpdate(t, tracker, []Delta{{0, 5, -1}, {1, 6, 1}})
	if _, frontiers := tracker.Frontiers(); !reflect.DeepEqual(frontiers, []int64{-1, 6, 7}) {
		t.Fatalf("frontiers = %v", frontiers)
	}
}

func TestExistingEntryJustifiesItsOwnIncrement(t *testing.T) {
	tracker, err := NewTracker(2, []Edge{{0, 1, 2}}, []int{0})
	if err != nil {
		t.Fatal(err)
	}

	mustUpdate(t, tracker, []Delta{{0, 4, 1}})
	mustUpdate(t, tracker, []Delta{{1, 6, 1}, {1, 6, 1}})

	frontier, err := tracker.Frontier(1)
	if err != nil || frontier != 6 {
		t.Fatalf("Frontier(1) = (%d,%v)", frontier, err)
	}
}

func TestDuplicateKeysNetDeltaOrderIndependent(t *testing.T) {
	first, err := NewTracker(1, nil, []int{0})
	if err != nil {
		t.Fatal(err)
	}
	mustUpdate(t, first, []Delta{{0, 3, 1}})
	mustUpdate(t, first, []Delta{{0, 3, 1}, {0, 3, -2}})

	second, err := NewTracker(1, nil, []int{0})
	if err != nil {
		t.Fatal(err)
	}
	mustUpdate(t, second, []Delta{{0, 3, 1}})
	mustUpdate(t, second, []Delta{{0, 3, -2}, {0, 3, 1}})

	if !reflect.DeepEqual(first.snapshotLedger(), second.snapshotLedger()) {
		t.Fatalf("ledgers differ: %v vs %v", first.snapshotLedger(), second.snapshotLedger())
	}

	fresh, err := NewTracker(1, nil, []int{0})
	if err != nil {
		t.Fatal(err)
	}
	assertRejection(t, fresh, []Delta{{0, 3, 1}, {0, 3, -2}}, ErrNegativeCount, 0, 3)
}

func TestRejectionPriority(t *testing.T) {
	tracker, err := NewTracker(2, []Edge{{0, 1, 1}}, []int{0})
	if err != nil {
		t.Fatal(err)
	}

	mustUpdate(t, tracker, []Delta{{0, 5, 1}})

	assertRejection(t, tracker, []Delta{
		{2, 0, 1},
		{1, 5, -1},
		{1, 0, 1},
	}, ErrInvalidArgument, 0, 0)

	assertRejection(t, tracker, []Delta{
		{1, 5, -1},
		{1, 0, 1},
	}, ErrNegativeCount, 1, 5)

	assertRejection(t, tracker, []Delta{{1, 0, 1}}, ErrCausalViolation, 1, 0)
}

func TestCountLimit(t *testing.T) {
	tracker, err := NewTracker(1, nil, []int{0})
	if err != nil {
		t.Fatal(err)
	}

	key := ledgerKey{position: 0, time: 0}
	tracker.ledger[key] = 1_000_000_000_000
	tracker.active.add(0, 0)
	assertRejection(t, tracker, []Delta{{0, 0, 1}}, ErrInvalidArgument, 0, 0)
}
