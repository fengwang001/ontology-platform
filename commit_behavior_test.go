package ontology

import (
	"sync"
	"testing"
)

func TestCapacityOneMultipleKeysEvictsWithActiveAndClearsWithoutActive(t *testing.T) {
	manager, _ := NewSnapshotCommitManager(1, 1)
	older := manager.Begin()
	writer := manager.Begin()
	result := manager.Commit(writer, []int64{10, 20})

	if result.Kind != CommitOk || result.Timestamp != 3 {
		t.Fatalf("active multi-key commit = %#v", result)
	}
	state := manager.Snapshot()
	if state.Watermark != 3 || len(state.Buckets[0]) != 1 || state.Buckets[0][0] != (TableEntryView{Key: 20, Timestamp: 3}) {
		t.Fatalf("active multi-key state = %#v", state)
	}

	noActive, _ := NewSnapshotCommitManager(1, 1)
	onlyWriter := noActive.Begin()
	result = noActive.Commit(onlyWriter, []int64{10, 20})
	state = noActive.Snapshot()
	if result.Timestamp != 2 || state.Watermark != 0 || len(state.Buckets[0]) != 1 ||
		state.Buckets[0][0] != (TableEntryView{Key: 20, Timestamp: 2}) {
		t.Fatalf("no-active multi-key result = %#v state = %#v", result, state)
	}

	if older != 1 {
		t.Fatalf("unexpected older transaction %d", older)
	}
}

func TestExistingEntryUpdatesTimestampWithoutUsingNewSlot(t *testing.T) {
	manager, _ := NewSnapshotCommitManager(1, 1)
	holder := manager.Begin()
	manager.Commit(holder, []int64{42})
	updater := manager.Begin()
	result := manager.Commit(updater, []int64{42, 42})

	state := manager.Snapshot()
	if result.Timestamp != 4 || state.Watermark != 0 || len(state.Buckets[0]) != 1 ||
		state.Buckets[0][0] != (TableEntryView{Key: 42, Timestamp: 4}) {
		t.Fatalf("update result = %#v state = %#v", result, state)
	}
}

func TestConcurrentCallsAreSerializable(t *testing.T) {
	manager, _ := NewSnapshotCommitManager(7, 3)
	const goroutines = 16
	const commits = 25

	var waitGroup sync.WaitGroup
	for worker := 0; worker < goroutines; worker++ {
		waitGroup.Add(1)
		go func(worker int) {
			defer waitGroup.Done()
			for index := 0; index < commits; index++ {
				sequence := manager.Begin()
				key := int64((worker*commits + index) % 50)
				switch index % 5 {
				case 0:
					manager.Abort(sequence)
				case 1:
					manager.Commit(sequence, nil)
				default:
					result := manager.Commit(sequence, []int64{key})
					if result.Kind == CommitOk && result.Timestamp <= sequence {
						t.Errorf("commit timestamp %d not after begin %d", result.Timestamp, sequence)
					}
				}
			}
		}(worker)
	}
	waitGroup.Wait()

	state := manager.Snapshot()
	if len(state.Active) != 0 {
		t.Fatalf("active transactions remain: %v", state.Active)
	}
	for bucketID, bucket := range state.Buckets {
		if len(bucket) > 3 {
			t.Fatalf("bucket %d has %d entries", bucketID, len(bucket))
		}
		seen := map[int64]bool{}
		for _, entry := range bucket {
			if seen[entry.Key] || entry.Timestamp > state.NextTimestamp {
				t.Fatalf("invalid bucket %d: %#v", bucketID, bucket)
			}
			seen[entry.Key] = true
		}
	}
}

func TestProbeCountIsLocalAndBounded(t *testing.T) {
	const buckets = 3
	const capacity = 4
	manager, _ := NewSnapshotCommitManager(buckets, capacity)
	for index := 0; index < buckets*capacity; index++ {
		sequence := manager.Begin()
		manager.Commit(sequence, []int64{int64(index)})
	}

	writer := manager.Begin()
	manager.resetProbeCount()
	keys := []int64{0, 3, 6, 1}
	manager.Commit(writer, keys)
	probes := manager.resetProbeCount()
	limit := int64(4 * len(keys) * capacity)
	if probes > limit {
		t.Fatalf("probes = %d, limit = %d", probes, limit)
	}

	state := manager.Snapshot()
	for _, key := range keys {
		bucketID := int(key % buckets)
		if len(state.Buckets[bucketID]) > capacity {
			t.Fatalf("bucket %d exceeded capacity: %#v", bucketID, state.Buckets[bucketID])
		}
	}
	if probes == 0 {
		t.Fatal("expected non-empty local probe count")
	}
}
