package ontology

import (
	"sync"
	"testing"
)

func entriesMap(snapshot Snapshot) map[int64]int64 {
	entries := make(map[int64]int64, len(snapshot.Entries))
	for _, entry := range snapshot.Entries {
		entries[entry.Key] = entry.Timestamp
	}
	return entries
}

func requireResult(t *testing.T, manager *CommitManager, transaction int64, keys []int64, want CommitResult) {
	t.Helper()
	got := manager.Commit(transaction, keys)
	t.Logf("input Commit(s=%d, keys=%v) => output=%+v, basis=%s", transaction, keys, got, got.Reason)
	if got != want {
		t.Fatalf("Commit(%d, %v) = %+v, want %+v", transaction, keys, got, want)
	}
}

func TestSpecExampleAndConflictBeforeWatermark(t *testing.T) {
	manager, err := NewCommitManager(1, 2)
	if err != nil {
		t.Fatal(err)
	}
	transactions := []int64{manager.Begin(), manager.Begin(), manager.Begin(), manager.Begin()}
	t.Logf("input four Begin calls => outputs=%v, basis=shared timestamp counter", transactions)

	requireResult(t, manager, transactions[1], []int64{10}, CommitResult{true, 5, ReasonCommitted})
	requireResult(t, manager, transactions[2], []int64{20}, CommitResult{true, 6, ReasonCommitted})
	requireResult(t, manager, transactions[3], []int64{30}, CommitResult{true, 7, ReasonCommitted})
	snapshot := manager.Snapshot()
	if got := entriesMap(snapshot); got[20] != 6 || got[30] != 7 || len(got) != 2 || snapshot.Watermark != 5 {
		t.Fatalf("snapshot after eviction = %+v", snapshot)
	}

	requireResult(t, manager, transactions[0], []int64{40}, CommitResult{false, 0, ReasonWatermark})
}

func TestConflictReportedBeforeWatermark(t *testing.T) {
	manager, _ := NewCommitManager(1, 2)
	first := manager.Begin()
	second := manager.Begin()
	firstWriter := manager.Begin()
	secondWriter := manager.Begin()
	evicter := manager.Begin()
	requireResult(t, manager, firstWriter, []int64{10}, CommitResult{true, 6, ReasonCommitted})
	requireResult(t, manager, secondWriter, []int64{20}, CommitResult{true, 7, ReasonCommitted})
	requireResult(t, manager, evicter, []int64{30}, CommitResult{true, 8, ReasonCommitted})
	if snapshot := manager.Snapshot(); snapshot.Watermark != 6 {
		t.Fatalf("watermark = %d, want 6", snapshot.Watermark)
	}
	requireResult(t, manager, first, []int64{20, 40}, CommitResult{false, 0, ReasonConflict})
	requireResult(t, manager, second, []int64{40}, CommitResult{false, 0, ReasonWatermark})
}

func TestWatermarkBoundaryAndEmptyWriteSet(t *testing.T) {
	manager, _ := NewCommitManager(1, 1)
	oldForAbort := manager.Begin()
	oldForEmpty := manager.Begin()
	writer := manager.Begin()
	requireResult(t, manager, writer, []int64{10}, CommitResult{true, 4, ReasonCommitted})
	if snapshot := manager.Snapshot(); snapshot.Watermark != 0 {
		t.Fatalf("watermark before eviction = %d, want 0", snapshot.Watermark)
	}

	belowWatermark := manager.Begin()
	requireResult(t, manager, belowWatermark, []int64{20}, CommitResult{true, 6, ReasonCommitted})
	snapshot := manager.Snapshot()
	if snapshot.Watermark != 4 {
		t.Fatalf("watermark after eviction = %d, want 4", snapshot.Watermark)
	}
	requireResult(t, manager, oldForAbort, []int64{40}, CommitResult{false, 0, ReasonWatermark})
	before := manager.Snapshot()
	requireResult(t, manager, oldForEmpty, nil, CommitResult{true, 0, ReasonCommitted})
	after := manager.Snapshot()
	if after.TimestampCounter != before.TimestampCounter || after.Watermark != before.Watermark || len(after.Entries) != len(before.Entries) {
		t.Fatalf("invalid empty commit changed state: before=%+v after=%+v", before, after)
	}
}

func TestNoRemainingActiveClearsFullBucketWithoutWatermark(t *testing.T) {
	manager, _ := NewCommitManager(1, 2)
	first := manager.Begin()
	second := manager.Begin()
	requireResult(t, manager, first, []int64{10}, CommitResult{true, 3, ReasonCommitted})
	requireResult(t, manager, second, []int64{20}, CommitResult{true, 4, ReasonCommitted})
	transaction := manager.Begin()
	requireResult(t, manager, transaction, []int64{30}, CommitResult{true, 6, ReasonCommitted})
	snapshot := manager.Snapshot()
	if got := entriesMap(snapshot); len(got) != 1 || got[30] != 6 || snapshot.Watermark != 0 {
		t.Fatalf("snapshot = %+v", snapshot)
	}
}

func TestEvictionTieUsesSmallerKey(t *testing.T) {
	manager, _ := NewCommitManager(1, 2)
	old := manager.Begin()
	writer := manager.Begin()
	requireResult(t, manager, writer, []int64{10, 20}, CommitResult{true, 3, ReasonCommitted})
	firstEvicter := manager.Begin()
	requireResult(t, manager, firstEvicter, []int64{30}, CommitResult{true, 5, ReasonCommitted})
	snapshot := manager.Snapshot()
	if got := entriesMap(snapshot); got[20] != 3 || got[30] != 5 || snapshot.Watermark != 3 {
		t.Fatalf("snapshot = %+v entries=%v", snapshot, got)
	}
	if !manager.Abort(old) {
		t.Fatal("Abort(old) = false")
	}
}

func TestMultipleKeysSameBucketA1WithOlderActive(t *testing.T) {
	manager, _ := NewCommitManager(1, 1)
	older := manager.Begin()
	writer := manager.Begin()
	requireResult(t, manager, writer, []int64{20, 10, 10}, CommitResult{true, 3, ReasonCommitted})
	snapshot := manager.Snapshot()
	if got := entriesMap(snapshot); len(got) != 1 || got[20] != 3 || snapshot.Watermark != 3 {
		t.Fatalf("snapshot = %+v entries=%v", snapshot, got)
	}
	manager.Abort(older)
}

func TestMultipleKeysSameBucketA1WithoutOtherActive(t *testing.T) {
	manager, _ := NewCommitManager(1, 1)
	writer := manager.Begin()
	requireResult(t, manager, writer, []int64{20, 10, 10}, CommitResult{true, 2, ReasonCommitted})
	snapshot := manager.Snapshot()
	if got := entriesMap(snapshot); len(got) != 1 || got[20] != 2 || snapshot.Watermark != 0 {
		t.Fatalf("snapshot = %+v entries=%v", snapshot, got)
	}
}

func TestExactFirstCommitterWinsWhileNoEviction(t *testing.T) {
	manager, _ := NewCommitManager(1, 16)
	older := manager.Begin()
	writer := manager.Begin()
	requireResult(t, manager, writer, []int64{10, 11, 12}, CommitResult{true, 3, ReasonCommitted})
	requireResult(t, manager, older, []int64{12, 20}, CommitResult{false, 0, ReasonConflict})
	unrelated := manager.Begin()
	requireResult(t, manager, unrelated, []int64{13}, CommitResult{true, 5, ReasonCommitted})
	snapshot := manager.Snapshot()
	if snapshot.Watermark != 0 {
		t.Fatalf("watermark = %d, want 0", snapshot.Watermark)
	}
	if got := entriesMap(snapshot); got[10] != 3 || got[11] != 3 || got[12] != 3 || got[13] != 5 {
		t.Fatalf("entries=%v snapshot=%+v", got, snapshot)
	}
}

func TestExistingEntryUpdatesWithoutNewSlot(t *testing.T) {
	manager, _ := NewCommitManager(1, 2)
	first := manager.Begin()
	second := manager.Begin()
	requireResult(t, manager, first, []int64{10}, CommitResult{true, 3, ReasonCommitted})
	requireResult(t, manager, second, []int64{20}, CommitResult{true, 4, ReasonCommitted})
	updater := manager.Begin()
	probesBefore := manager.probesSinceCommit()
	requireResult(t, manager, updater, []int64{10}, CommitResult{true, 6, ReasonCommitted})
	if probes := manager.probesSinceCommit(); probes > 4*1*2 || probes <= probesBefore {
		t.Fatalf("probe count = %d", probes)
	}
	snapshot := manager.Snapshot()
	if got := entriesMap(snapshot); len(got) != 2 || got[10] != 6 || got[20] != 4 {
		t.Fatalf("entries=%v snapshot=%+v", got, snapshot)
	}
}

func TestRejectionsAndAbortPreserveState(t *testing.T) {
	manager, _ := NewCommitManager(1, 1)
	transaction := manager.Begin()
	before := manager.Snapshot()
	if result := manager.Commit(transaction, make([]int64, 65)); result.Reason != ReasonTooManyKeys {
		t.Fatalf("result=%+v", result)
	}
	if result := manager.Commit(transaction, []int64{-1}); result.Reason != ReasonInvalidKey {
		t.Fatalf("result=%+v", result)
	}
	if result := manager.Commit(transaction+10, []int64{1}); result.Reason != ReasonInvalidTxn {
		t.Fatalf("result=%+v", result)
	}
	if manager.Abort(transaction + 10) {
		t.Fatal("Abort unknown transaction succeeded")
	}
	after := manager.Snapshot()
	if after.TimestampCounter != before.TimestampCounter || len(after.Active) != 1 || after.Active[0] != transaction {
		t.Fatalf("before=%+v after=%+v", before, after)
	}
	if !manager.Abort(transaction) || manager.Abort(transaction) {
		t.Fatal("Abort was not idempotent at API boundary")
	}
}

func TestInvalidConfiguration(t *testing.T) {
	for _, configuration := range [][2]int{{0, 1}, {1025, 1}, {1, 0}, {1, 17}} {
		if manager, err := NewCommitManager(configuration[0], configuration[1]); err == nil || manager != nil {
			t.Fatalf("configuration %+v was accepted", configuration)
		}
	}
}

func TestConcurrentCallsAreSerializable(t *testing.T) {
	manager, _ := NewCommitManager(8, 4)
	const goroutines = 32
	var waitGroup sync.WaitGroup
	for goroutine := 0; goroutine < goroutines; goroutine++ {
		waitGroup.Add(1)
		go func(id int) {
			defer waitGroup.Done()
			transaction := manager.Begin()
			key := int64(id * 3)
			result := manager.Commit(transaction, []int64{key, key + 1})
			if !result.Committed {
				if !manager.Abort(transaction) {
					t.Errorf("abort transaction %d after %+v failed", transaction, result)
				}
				return
			}
			if result.Timestamp <= transaction {
				t.Errorf("transaction %d got %+v", transaction, result)
			}
		}(goroutine)
	}
	waitGroup.Wait()
	snapshot := manager.Snapshot()
	if len(snapshot.Active) != 0 || snapshot.Watermark > snapshot.TimestampCounter {
		t.Fatalf("invalid final snapshot: %+v", snapshot)
	}
}
