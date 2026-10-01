package recovery

import (
	"errors"
	"reflect"
	"testing"
)

// mustAppend appends all records, failing the test on the first error.
func mustAppend(t *testing.T, l *Log, recs ...Record) {
	t.Helper()
	for _, r := range recs {
		if err := l.Append(r); err != nil {
			t.Fatalf("Append(%+v) failed: %v", r, err)
		}
	}
}

// TestPageFlushedDuringCheckpoint covers a page present in the checkpoint
// DPT snapshot that is flushed after the BeginCkpt: the PageFlush record
// beyond the begin LSN must remove it from the reconstructed DPT.
func TestPageFlushedDuringCheckpoint(t *testing.T) {
	l := NewLog()
	mustAppend(t, l,
		Update(10, 1, 1),
		BeginCkpt(20),
		EndCkpt(30, 20,
			map[PageID]LSN{1: 10, 2: 5},
			map[TxnID]ATTEntry{1: {Status: StatusRunning, LastLSN: 10}}),
		PageFlush(40, 2),
	)

	got := l.Analyze()
	want := Result{
		NeedRedo: true,
		RedoLSN:  10,
		DPT:      []DirtyPageEntry{{Page: 1, RecLSN: 10}},
		ATT:      []ActiveTxnEntry{{Txn: 1, Entry: ATTEntry{Status: StatusRunning, LastLSN: 10}}},
		Failed:   []TxnID{1},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Analyze()=%+v, want %+v", got, want)
	}
}

// TestPageUpdatedAgainAfterFlush covers a page flushed and then updated
// again: it re-enters the DPT with the recLSN of the new update.
func TestPageUpdatedAgainAfterFlush(t *testing.T) {
	l := NewLog()
	mustAppend(t, l,
		Update(10, 1, 1),
		PageFlush(20, 1),
		Update(30, 1, 1),
	)

	got := l.Analyze()
	want := Result{
		NeedRedo: true,
		RedoLSN:  30,
		DPT:      []DirtyPageEntry{{Page: 1, RecLSN: 30}},
		ATT:      []ActiveTxnEntry{{Txn: 1, Entry: ATTEntry{Status: StatusRunning, LastLSN: 30}}},
		Failed:   []TxnID{1},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Analyze()=%+v, want %+v", got, want)
	}
}

// TestCommittedNotEndedTxn covers a committed but not yet ended transaction:
// it stays in the ATT as Committed but is not a failed transaction.
func TestCommittedNotEndedTxn(t *testing.T) {
	l := NewLog()
	mustAppend(t, l,
		Update(10, 1, 1),
		Commit(20, 1),
		Update(30, 2, 2),
		Abort(40, 2),
	)

	got := l.Analyze()
	want := Result{
		NeedRedo: true,
		RedoLSN:  10,
		DPT: []DirtyPageEntry{
			{Page: 1, RecLSN: 10},
			{Page: 2, RecLSN: 30},
		},
		ATT: []ActiveTxnEntry{
			{Txn: 1, Entry: ATTEntry{Status: StatusCommitted, LastLSN: 20}},
			{Txn: 2, Entry: ATTEntry{Status: StatusAborting, LastLSN: 40}},
		},
		Failed: []TxnID{2},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Analyze()=%+v, want %+v", got, want)
	}
}

// TestIncompleteCheckpointIgnored covers a BeginCkpt without EndCkpt:
// analysis falls back to the previous complete checkpoint.
func TestIncompleteCheckpointIgnored(t *testing.T) {
	l := NewLog()
	mustAppend(t, l,
		Update(10, 1, 1),
		BeginCkpt(20),
		EndCkpt(30, 20,
			map[PageID]LSN{1: 10},
			map[TxnID]ATTEntry{1: {Status: StatusRunning, LastLSN: 10}}),
		Update(40, 2, 2),
		BeginCkpt(50), // incomplete: no matching EndCkpt
		Update(60, 2, 3),
	)

	got := l.Analyze()
	want := Result{
		NeedRedo: true,
		RedoLSN:  10,
		DPT: []DirtyPageEntry{
			{Page: 1, RecLSN: 10},
			{Page: 2, RecLSN: 40},
			{Page: 3, RecLSN: 60},
		},
		ATT: []ActiveTxnEntry{
			{Txn: 1, Entry: ATTEntry{Status: StatusRunning, LastLSN: 10}},
			{Txn: 2, Entry: ATTEntry{Status: StatusRunning, LastLSN: 60}},
		},
		Failed: []TxnID{1, 2},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Analyze()=%+v, want %+v", got, want)
	}
}

// TestNoCheckpoint covers a log without any checkpoint: analysis scans the
// whole log starting from empty tables.
func TestNoCheckpoint(t *testing.T) {
	l := NewLog()
	mustAppend(t, l,
		Update(1, 1, 7),
		Update(2, 2, 7),
		Commit(3, 1),
		End(4, 1),
	)

	got := l.Analyze()
	want := Result{
		NeedRedo: true,
		RedoLSN:  1,
		DPT:      []DirtyPageEntry{{Page: 7, RecLSN: 1}},
		ATT:      []ActiveTxnEntry{{Txn: 2, Entry: ATTEntry{Status: StatusRunning, LastLSN: 2}}},
		Failed:   []TxnID{2},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Analyze()=%+v, want %+v", got, want)
	}
}

// TestEmptyDPT covers an empty dirty page table: no redo is required.
func TestEmptyDPT(t *testing.T) {
	l := NewLog()
	mustAppend(t, l,
		Update(10, 1, 1),
		PageFlush(20, 1),
		Commit(30, 1),
		End(40, 1),
	)

	got := l.Analyze()
	want := Result{
		NeedRedo: false,
		DPT:      []DirtyPageEntry{},
		ATT:      []ActiveTxnEntry{},
		Failed:   []TxnID{},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Analyze()=%+v, want %+v", got, want)
	}
}

// TestUpdateKeepsStatus covers the Update rule: for a transaction already
// in the ATT only the last LSN is refreshed, the status is preserved.
func TestUpdateKeepsStatus(t *testing.T) {
	l := NewLog()
	mustAppend(t, l,
		Update(10, 1, 1),
		Abort(20, 1),
		Update(30, 1, 2), // undo write during rollback: status stays Aborting
	)

	got := l.Analyze()
	want := Result{
		NeedRedo: true,
		RedoLSN:  10,
		DPT: []DirtyPageEntry{
			{Page: 1, RecLSN: 10},
			{Page: 2, RecLSN: 30},
		},
		ATT:    []ActiveTxnEntry{{Txn: 1, Entry: ATTEntry{Status: StatusAborting, LastLSN: 30}}},
		Failed: []TxnID{1},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Analyze()=%+v, want %+v", got, want)
	}
}

// TestSnapshotTxnCountsAsSeen covers the Append rule that transactions in a
// checkpoint ATT snapshot count as appeared.
func TestSnapshotTxnCountsAsSeen(t *testing.T) {
	l := NewLog()
	mustAppend(t, l,
		BeginCkpt(10),
		EndCkpt(20, 10,
			map[PageID]LSN{},
			map[TxnID]ATTEntry{5: {Status: StatusRunning, LastLSN: 3}}),
		Commit(30, 5),
	)

	got := l.Analyze()
	want := Result{
		NeedRedo: false,
		DPT:      []DirtyPageEntry{},
		ATT:      []ActiveTxnEntry{{Txn: 5, Entry: ATTEntry{Status: StatusCommitted, LastLSN: 30}}},
		Failed:   []TxnID{},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Analyze()=%+v, want %+v", got, want)
	}
}

// TestAppendRejections covers every rejection reason, the fixed reporting
// order, and that rejected records leave the log unchanged.
func TestAppendRejections(t *testing.T) {
	t.Run("LSN not increasing", func(t *testing.T) {
		l := NewLog()
		mustAppend(t, l, Update(10, 1, 1))
		before := l.Records()
		if err := l.Append(Update(10, 2, 2)); !errors.Is(err, ErrLSNNotIncreasing) {
			t.Fatalf("err=%v, want ErrLSNNotIncreasing", err)
		}
		if err := l.Append(Update(5, 2, 2)); !errors.Is(err, ErrLSNNotIncreasing) {
			t.Fatalf("err=%v, want ErrLSNNotIncreasing", err)
		}
		if !reflect.DeepEqual(l.Records(), before) {
			t.Fatalf("rejected record changed the log")
		}
	})

	t.Run("EndCkpt unknown begin", func(t *testing.T) {
		l := NewLog()
		mustAppend(t, l, BeginCkpt(10))
		before := l.Records()
		err := l.Append(EndCkpt(20, 15, nil, nil))
		if !errors.Is(err, ErrEndCkptUnknownBegin) {
			t.Fatalf("err=%v, want ErrEndCkptUnknownBegin", err)
		}
		if !reflect.DeepEqual(l.Records(), before) {
			t.Fatalf("rejected record changed the log")
		}
	})

	t.Run("EndCkpt duplicate", func(t *testing.T) {
		l := NewLog()
		mustAppend(t, l, BeginCkpt(10), EndCkpt(20, 10, nil, nil))
		before := l.Records()
		err := l.Append(EndCkpt(30, 10, nil, nil))
		if !errors.Is(err, ErrEndCkptDuplicate) {
			t.Fatalf("err=%v, want ErrEndCkptDuplicate", err)
		}
		if !reflect.DeepEqual(l.Records(), before) {
			t.Fatalf("rejected record changed the log")
		}
	})

	t.Run("references ended txn", func(t *testing.T) {
		l := NewLog()
		mustAppend(t, l, Update(10, 1, 1), End(20, 1))
		before := l.Records()
		if err := l.Append(Update(30, 1, 2)); !errors.Is(err, ErrTxnAlreadyEnded) {
			t.Fatalf("err=%v, want ErrTxnAlreadyEnded", err)
		}
		if err := l.Append(Commit(30, 1)); !errors.Is(err, ErrTxnAlreadyEnded) {
			t.Fatalf("err=%v, want ErrTxnAlreadyEnded", err)
		}
		if !reflect.DeepEqual(l.Records(), before) {
			t.Fatalf("rejected record changed the log")
		}
	})

	t.Run("txn never seen", func(t *testing.T) {
		l := NewLog()
		before := l.Records()
		if err := l.Append(Commit(10, 9)); !errors.Is(err, ErrTxnNeverSeen) {
			t.Fatalf("err=%v, want ErrTxnNeverSeen", err)
		}
		if err := l.Append(Abort(10, 9)); !errors.Is(err, ErrTxnNeverSeen) {
			t.Fatalf("err=%v, want ErrTxnNeverSeen", err)
		}
		if err := l.Append(End(10, 9)); !errors.Is(err, ErrTxnNeverSeen) {
			t.Fatalf("err=%v, want ErrTxnNeverSeen", err)
		}
		if !reflect.DeepEqual(l.Records(), before) {
			t.Fatalf("rejected record changed the log")
		}
	})

	t.Run("only first reason reported", func(t *testing.T) {
		l := NewLog()
		mustAppend(t, l, Update(10, 1, 1), End(20, 1))
		// Violates rules 1 and 3: rule 1 wins.
		if err := l.Append(Update(5, 1, 2)); !errors.Is(err, ErrLSNNotIncreasing) {
			t.Fatalf("err=%v, want ErrLSNNotIncreasing", err)
		}
		// Violates rules 2 and 3 is impossible for EndCkpt (no txn ref);
		// violates rules 1 and 2: rule 1 wins.
		if err := l.Append(EndCkpt(5, 3, nil, nil)); !errors.Is(err, ErrLSNNotIncreasing) {
			t.Fatalf("err=%v, want ErrLSNNotIncreasing", err)
		}
		// EndCkpt with unknown begin at a fresh LSN: rule 2 reported.
		if err := l.Append(EndCkpt(30, 3, nil, nil)); !errors.Is(err, ErrEndCkptUnknownBegin) {
			t.Fatalf("err=%v, want ErrEndCkptUnknownBegin", err)
		}
	})
}

// TestReplayDeterminism: the same append sequence replayed into a fresh log
// yields exactly the same analysis result.
func TestReplayDeterminism(t *testing.T) {
	recs := []Record{
		Update(10, 1, 1),
		BeginCkpt(20),
		Update(30, 2, 2),
		EndCkpt(40, 20,
			map[PageID]LSN{1: 10},
			map[TxnID]ATTEntry{1: {Status: StatusRunning, LastLSN: 10}}),
		PageFlush(50, 1),
		Abort(60, 2),
		Update(70, 2, 1),
	}
	l1 := NewLog()
	mustAppend(t, l1, recs...)
	l2 := NewLog()
	mustAppend(t, l2, l1.Records()...)
	if got, want := l1.Analyze(), l2.Analyze(); !reflect.DeepEqual(got, want) {
		t.Fatalf("replay mismatch: %+v vs %+v", got, want)
	}
}
