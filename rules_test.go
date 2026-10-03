package ontology

import "testing"

func TestInvalidConfigAndRejectionsDoNotChangeState(t *testing.T) {
	for _, config := range [][2]int{{0, 1}, {1025, 1}, {1, 0}, {1, 17}} {
		if _, err := NewSnapshotCommitManager(config[0], config[1]); err != ErrInvalidConfig {
			t.Fatalf("config %v = %v", config, err)
		}
	}

	manager, _ := NewSnapshotCommitManager(1, 1)
	s := manager.Begin()
	before := manager.Snapshot()

	if result := manager.Commit(99, nil); result != (CommitResult{Kind: CommitRejected, Reason: RejectUnknownTransaction}) {
		t.Fatalf("unknown = %#v", result)
	}
	if result := manager.Commit(s, append(make([]int64, 0, 65), make([]int64, 65)...)); result.Reason != RejectTooManyKeys {
		t.Fatalf("too many = %#v", result)
	}
	if result := manager.Commit(s, []int64{-1}); result.Reason != RejectInvalidKey {
		t.Fatalf("negative key = %#v", result)
	}
	if result := manager.Commit(s, []int64{maxKey + 1}); result.Reason != RejectInvalidKey {
		t.Fatalf("large key = %#v", result)
	}
	if err := manager.Abort(99); err != ErrUnknownTransaction {
		t.Fatalf("abort unknown = %v", err)
	}

	after := manager.Snapshot()
	if after.NextTimestamp != before.NextTimestamp || after.Watermark != before.Watermark || len(after.Active) != 1 {
		t.Fatalf("state changed after rejection: before %#v after %#v", before, after)
	}
}

func TestAbortsAndCommitAbortsPreserveTimestampTableWatermark(t *testing.T) {
	manager, _ := NewSnapshotCommitManager(1, 1)
	older := manager.Begin()
	conflicter := manager.Begin()
	firstWriter := manager.Begin()
	secondWriter := manager.Begin()
	manager.Commit(firstWriter, []int64{10})
	manager.Commit(secondWriter, []int64{20})

	before := manager.Snapshot()
	if result := manager.Commit(conflicter, []int64{20}); result.Kind != CommitConflict {
		t.Fatalf("conflict result = %#v", result)
	}
	if result := manager.Commit(older, []int64{30}); result.Kind != CommitWatermark {
		t.Fatalf("watermark result = %#v", result)
	}
	after := manager.Snapshot()

	if after.NextTimestamp != before.NextTimestamp || after.Watermark != before.Watermark {
		t.Fatalf("abort changed timestamps before=%#v after=%#v", before, after)
	}
	if !equalBuckets(after.Buckets, before.Buckets) {
		t.Fatalf("abort changed table before=%#v after=%#v", before.Buckets, after.Buckets)
	}

	open := manager.Begin()
	beforeAbort := manager.Snapshot()
	if err := manager.Abort(open); err != nil {
		t.Fatal(err)
	}
	afterAbort := manager.Snapshot()
	if afterAbort.NextTimestamp != beforeAbort.NextTimestamp ||
		afterAbort.Watermark != beforeAbort.Watermark ||
		!equalBuckets(afterAbort.Buckets, beforeAbort.Buckets) {
		t.Fatalf("Abort changed shared state before=%#v after=%#v", beforeAbort, afterAbort)
	}
}

func TestEmptyCommitUsesNoTimestampAndIgnoresWatermark(t *testing.T) {
	manager, _ := NewSnapshotCommitManager(1, 1)
	oldest := manager.Begin()
	evictor := manager.Begin()
	extra := manager.Begin()

	manager.Commit(evictor, []int64{10})
	manager.Commit(extra, []int64{20})
	if state := manager.Snapshot(); state.NextTimestamp != 5 || state.Watermark != 4 {
		t.Fatalf("watermark setup state = %#v", state)
	}

	result := manager.Commit(oldest, nil)
	if result != (CommitResult{Kind: CommitOk}) {
		t.Fatalf("empty commit = %#v", result)
	}
	state := manager.Snapshot()
	if state.NextTimestamp != 5 || state.Watermark != 4 || len(state.Active) != 0 {
		t.Fatalf("empty commit state = %#v", state)
	}
}

func TestWatermarkBelowStartPassesAndAboveStartAborts(t *testing.T) {
	manager, _ := NewSnapshotCommitManager(1, 1)
	older := manager.Begin()
	firstWriter := manager.Begin()
	secondWriter := manager.Begin()
	manager.Commit(firstWriter, []int64{10})
	manager.Commit(secondWriter, []int64{20})

	young := manager.Begin()
	if result := manager.Commit(young, []int64{99}); result.Kind != CommitOk || result.Timestamp != 7 {
		t.Fatalf("watermark below start = %#v", result)
	}

	if result := manager.Commit(older, []int64{88}); result.Kind != CommitWatermark {
		t.Fatalf("watermark above start = %#v", result)
	}
}

func TestClearUsesRemainingMinimumAndNoActiveClearsWholeBucket(t *testing.T) {
	manager, _ := NewSnapshotCommitManager(1, 2)
	oldest := manager.Begin()
	firstWriter := manager.Begin()
	secondWriter := manager.Begin()
	manager.Commit(firstWriter, []int64{10})
	manager.Commit(secondWriter, []int64{20})

	finisher := manager.Begin()
	result := manager.Commit(finisher, []int64{30})
	if result.Timestamp != 7 {
		t.Fatalf("commit timestamp = %d", result.Timestamp)
	}
	if state := manager.Snapshot(); state.Watermark != 4 {
		t.Fatalf("with remaining active watermark = %d", state.Watermark)
	}

	manager.Abort(oldest)
	manager, _ = NewSnapshotCommitManager(1, 2)
	firstWriter = manager.Begin()
	secondWriter = manager.Begin()
	manager.Commit(firstWriter, []int64{10})
	manager.Commit(secondWriter, []int64{20})
	finisher = manager.Begin()
	result = manager.Commit(finisher, []int64{30})
	state := manager.Snapshot()
	if result.Timestamp != 6 || state.Watermark != 0 {
		t.Fatalf("no-active result = %#v state = %#v", result, state)
	}
	if len(state.Buckets[0]) != 1 || state.Buckets[0][0] != (TableEntryView{Key: 30, Timestamp: 6}) {
		t.Fatalf("whole bucket was not cleared: %#v", state.Buckets)
	}
}

func TestEvictionTieChoosesSmallerKey(t *testing.T) {
	manager, _ := NewSnapshotCommitManager(1, 2)
	oldest := manager.Begin()
	first := manager.Begin()
	manager.Commit(first, []int64{20, 10})
	manager.Commit(manager.Begin(), []int64{30})

	state := manager.Snapshot()
	if state.Watermark != 3 {
		t.Fatalf("watermark = %d", state.Watermark)
	}
	want := [][]TableEntryView{{{Key: 20, Timestamp: 3}, {Key: 30, Timestamp: 5}}}
	if !equalBuckets(state.Buckets, want) || len(state.Active) != 1 || state.Active[0] != oldest {
		t.Fatalf("state = %#v, want %#v", state.Buckets, want)
	}
}

func equalBuckets(got, want [][]TableEntryView) bool {
	if len(got) != len(want) {
		return false
	}
	for index := range got {
		if len(got[index]) != len(want[index]) {
			return false
		}
		for entry := range got[index] {
			if got[index][entry] != want[index][entry] {
				return false
			}
		}
	}
	return true
}
