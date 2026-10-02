package ontology

import (
	"errors"
	"reflect"
	"sync"
	"testing"
)

func name(subtask, checkpoint int) Name {
	return Name{Subtask: subtask, Checkpoint: checkpoint}
}

func mustWrite(t *testing.T, r *Registry, subtask, records int) {
	t.Helper()
	if err := r.Write(subtask, records); err != nil {
		t.Fatalf("Write(%d,%d) returned %v", subtask, records, err)
	}
}

func mustBarrier(t *testing.T, r *Registry, checkpoint int, want []Name) {
	t.Helper()
	got, err := r.Barrier(checkpoint)
	if err != nil || (len(got) != len(want)) {
		t.Fatalf("Barrier(%d) = %#v, %v; want %#v", checkpoint, got, err, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Barrier(%d) = %#v; want %#v", checkpoint, got, want)
		}
	}
}

func mustComplete(t *testing.T, r *Registry, checkpoint int, want []Name) {
	t.Helper()
	got, err := r.Complete(checkpoint)
	if err != nil || len(got) != len(want) {
		t.Fatalf("Complete(%d) = %#v, %v; want %#v", checkpoint, got, err, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Complete(%d) = %#v; want %#v", checkpoint, got, want)
		}
	}
}

func assertErrorIs(t *testing.T, got, want error) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Fatalf("error = %v; want %v", got, want)
	}
}

func TestSpecRestoreExample(t *testing.T) {
	r, err := New(2, 2)
	if err != nil {
		t.Fatal(err)
	}

	mustWrite(t, r, 0, 3)
	mustWrite(t, r, 1, 2)
	mustBarrier(t, r, 1, []Name{name(0, 1), name(1, 1)})
	mustWrite(t, r, 0, 4)
	mustBarrier(t, r, 2, []Name{name(0, 2)})
	mustComplete(t, r, 1, []Name{name(0, 1), name(1, 1)})
	mustWrite(t, r, 0, 1)
	mustWrite(t, r, 1, 7)
	mustBarrier(t, r, 3, []Name{name(0, 3), name(1, 3)})
	mustWrite(t, r, 1, 6)

	result, err := r.Restore(2, 1)
	if err != nil {
		t.Fatal(err)
	}
	wantResult := RestoreResult{
		Committed: []Name{name(0, 2)},
		Aborted:   []Name{name(0, 3), name(1, 3), name(1, 4)},
		Probes:    7,
	}
	if !reflect.DeepEqual(result, wantResult) {
		t.Fatalf("Restore = %#v; want %#v", result, wantResult)
	}
	if r.lastBarrier != 2 || r.lastNotify != 2 || r.life != 1 || r.parallelism != 1 {
		t.Fatalf("state = lb:%d ln:%d life:%d P:%d", r.lastBarrier, r.lastNotify, r.life, r.parallelism)
	}

	mustWrite(t, r, 0, 5)
	info, err := r.Txn(0, 3)
	if err != nil {
		t.Fatal(err)
	}
	if info.Status != OPEN || info.Epoch != 1 || info.Records != 5 || info.Life != 1 {
		t.Fatalf("reopened txn = %#v", info)
	}

	stats := r.Stats()
	if stats.CommittedRecords != 9 || stats.AbortedRecords != 14 || stats.OpenRecords != 5 || stats.Probes != 7 {
		t.Fatalf("stats = %#v", stats)
	}
}

func TestNamingBarrierAndSkippedNotifications(t *testing.T) {
	r, err := New(3, 2)
	if err != nil {
		t.Fatal(err)
	}

	mustWrite(t, r, 0, 2)
	mustWrite(t, r, 0, 3)
	mustWrite(t, r, 2, 4)
	info, err := r.Txn(0, 1)
	if err != nil || info.Status != OPEN || info.Records != 5 || info.Epoch != 0 {
		t.Fatalf("txn before barrier = %#v, %v", info, err)
	}

	mustBarrier(t, r, 1, []Name{name(0, 1), name(2, 1)})
	info, _ = r.Txn(0, 1)
	if info.Status != PREPARED {
		t.Fatalf("prepared status = %s", info.Status)
	}

	mustWrite(t, r, 0, 7)
	info, err = r.Txn(0, 2)
	if err != nil || info.Status != OPEN || info.Records != 7 {
		t.Fatalf("txn after barrier rename = %#v, %v", info, err)
	}
	if pending := r.Pending(); !reflect.DeepEqual(pending, []Name{name(0, 1), name(0, 2), name(2, 1)}) {
		t.Fatalf("pending = %#v", pending)
	}

	mustBarrier(t, r, 2, []Name{name(0, 2)})
	mustComplete(t, r, 2, []Name{name(0, 1), name(2, 1), name(0, 2)})
}

func TestNotificationErrors(t *testing.T) {
	r, _ := New(1, 2)
	mustWrite(t, r, 0, 1)
	mustBarrier(t, r, 1, []Name{name(0, 1)})

	_, err := r.Complete(0)
	assertErrorIs(t, err, ErrInvalidArgument)
	mustComplete(t, r, 1, []Name{name(0, 1)})
	_, err = r.Complete(1)
	assertErrorIs(t, err, ErrStaleNotification)
	_, err = r.Complete(0)
	assertErrorIs(t, err, ErrInvalidArgument)
	_, err = r.Complete(2)
	assertErrorIs(t, err, ErrFutureNotification)
	_, err = r.Barrier(0)
	assertErrorIs(t, err, ErrInvalidArgument)
	_, err = r.Barrier(3)
	assertErrorIs(t, err, ErrCheckpointOrder)
}

func TestRestoreCommitsWindowBeforeSweep(t *testing.T) {
	r, _ := New(1, 2)
	mustWrite(t, r, 0, 2)
	mustBarrier(t, r, 1, []Name{name(0, 1)})
	mustWrite(t, r, 0, 3)
	mustBarrier(t, r, 2, []Name{name(0, 2)})
	mustWrite(t, r, 0, 4)
	mustBarrier(t, r, 3, []Name{name(0, 3)})
	mustWrite(t, r, 0, 5)

	result, err := r.Restore(2, 1)
	if err != nil {
		t.Fatal(err)
	}
	want := RestoreResult{
		Committed: []Name{name(0, 1), name(0, 2)},
		Aborted:   []Name{name(0, 3), name(0, 4)},
		Probes:    4,
	}
	if !reflect.DeepEqual(result, want) {
		t.Fatalf("restore = %#v; want %#v", result, want)
	}
	if txn, _ := r.Txn(0, 2); txn.Status != COMMITTED {
		t.Fatalf("checkpoint 2 status = %s", txn.Status)
	}
}

func TestMissLimitsAndLegacyOpen(t *testing.T) {
	t.Run("M1 hit then miss and later leftover", func(t *testing.T) {
		r, _ := New(1, 1)
		mustWrite(t, r, 0, 2)
		mustBarrier(t, r, 1, []Name{name(0, 1)})
		mustBarrier(t, r, 2, []Name{})
		mustBarrier(t, r, 3, []Name{})
		mustWrite(t, r, 0, 4)
		mustBarrier(t, r, 4, []Name{name(0, 4)})
		mustBarrier(t, r, 5, []Name{})
		mustWrite(t, r, 0, 4)

		result, err := r.Restore(3, 1)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(result.Aborted, []Name{name(0, 4)}) || result.Probes != 2 {
			t.Fatalf("M1 restore = %#v", result)
		}
		if txn, _ := r.Txn(0, 6); txn.Status != OPEN || txn.Records != 4 {
			t.Fatalf("leftover txn = %#v", txn)
		}

		mustBarrier(t, r, 4, []Name{})
		mustBarrier(t, r, 5, []Name{})
		mustWrite(t, r, 0, 5)
		txn, _ := r.Txn(0, 6)
		if txn.Status != OPEN || txn.Epoch != 1 || txn.Records != 5 {
			t.Fatalf("reopened after legacy open = %#v", txn)
		}
		if stats := r.Stats(); stats.AbortedRecords != 8 {
			t.Fatalf("aborted records = %d; want 8", stats.AbortedRecords)
		}
	})

	t.Run("M2 two misses leave open beyond horizon", func(t *testing.T) {
		r, _ := New(1, 2)
		mustWrite(t, r, 0, 1)
		mustBarrier(t, r, 1, []Name{name(0, 1)})
		mustBarrier(t, r, 2, nil)
		mustBarrier(t, r, 3, nil)
		mustWrite(t, r, 0, 5)

		result, err := r.Restore(0, 1)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(result.Aborted, []Name{name(0, 1)}) || result.Probes != 3 {
			t.Fatalf("M2 restore = %#v", result)
		}
		if txn, _ := r.Txn(0, 4); txn.Status != OPEN || txn.Records != 5 {
			t.Fatalf("txn beyond horizon = %#v", txn)
		}

		mustBarrier(t, r, 1, []Name{})
		mustBarrier(t, r, 2, []Name{})
		mustBarrier(t, r, 3, []Name{})
		mustWrite(t, r, 0, 2)
		txn, _ := r.Txn(0, 4)
		if txn.Status != OPEN || txn.Epoch != 1 || txn.Records != 2 {
			t.Fatalf("reopened beyond horizon = %#v", txn)
		}
	})
}

func TestRestoreParallelismSweepRange(t *testing.T) {
	r, _ := New(1, 2)
	mustWrite(t, r, 0, 1)
	mustBarrier(t, r, 1, []Name{name(0, 1)})
	mustWrite(t, r, 0, 2)
	mustBarrier(t, r, 2, []Name{name(0, 2)})

	result, err := r.Restore(1, 2)
	if err != nil {
		t.Fatal(err)
	}
	want := RestoreResult{
		Committed: []Name{name(0, 1)},
		Aborted:   []Name{name(0, 2)},
		Probes:    5,
	}
	if !reflect.DeepEqual(result, want) {
		t.Fatalf("expansion restore = %#v; want %#v", result, want)
	}
}

func TestLegacyPreparedNotCommitted(t *testing.T) {
	r, _ := New(1, 3)
	for cp := 1; cp <= 5; cp++ {
		if cp == 2 || cp == 6 {
			mustWrite(t, r, 0, cp)
		}
		if cp == 2 {
			mustBarrier(t, r, cp, []Name{name(0, cp)})
		} else {
			mustBarrier(t, r, cp, []Name{})
		}
	}
	mustWrite(t, r, 0, 6)
	mustBarrier(t, r, 6, []Name{name(0, 6)})

	result, err := r.Restore(1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(result.Committed, []Name{}) || !reflect.DeepEqual(result.Aborted, []Name{name(0, 2)}) || result.Probes != 4 {
		t.Fatalf("restore = %#v", result)
	}

	mustBarrier(t, r, 2, []Name{})
	mustBarrier(t, r, 3, []Name{})
	mustBarrier(t, r, 4, []Name{})
	mustBarrier(t, r, 5, []Name{})
	mustBarrier(t, r, 6, []Name{})
	committed, err := r.Complete(6)
	if err != nil {
		t.Fatal(err)
	}
	if len(committed) != 0 {
		t.Fatalf("legacy prepared committed %#v", committed)
	}
	txn, _ := r.Txn(0, 6)
	if txn.Status != PREPARED || txn.Life != 0 {
		t.Fatalf("legacy txn = %#v", txn)
	}
}

type stateSnapshot struct {
	P, lb, ln, life int
	txns            map[Name]transaction
	prepared        []Name
	committed       int
	aborted         int
	probes          int
}

func snapshotState(r *Registry) stateSnapshot {
	r.mu.RLock()
	defer r.mu.RUnlock()
	txns := make(map[Name]transaction, len(r.txns))
	for key, value := range r.txns {
		txns[key] = value
	}
	return stateSnapshot{
		P: r.parallelism, lb: r.lastBarrier, ln: r.lastNotify, life: r.life,
		txns: txns, prepared: append([]Name(nil), r.prepared...),
		committed: r.committedRecords, aborted: r.abortedRecords, probes: r.probes,
	}
}

func TestRejectedOperationsDoNotChangeState(t *testing.T) {
	r, _ := New(2, 2)
	mustWrite(t, r, 0, 3)
	mustBarrier(t, r, 1, []Name{name(0, 1)})
	mustComplete(t, r, 1, []Name{name(0, 1)})
	before := snapshotState(r)

	invalidCalls := []func() error{
		func() error { return r.Write(-1, 1) },
		func() error { return r.Write(2, 1) },
		func() error { return r.Write(0, 0) },
		func() error { return r.Write(0, 1_000_001) },
		func() error { _, err := r.Barrier(0); return err },
		func() error { _, err := r.Barrier(3); return err },
		func() error { _, err := r.Complete(0); return err },
		func() error { _, err := r.Complete(1); return err },
		func() error { _, err := r.Complete(2); return err },
		func() error { _, err := r.Restore(-1, 1); return err },
		func() error { _, err := r.Restore(1, 0); return err },
		func() error { _, err := r.Restore(1, 65); return err },
		func() error { _, err := r.Restore(0, 1); return err },
		func() error { _, err := r.Restore(2, 1); return err },
	}
	for i, call := range invalidCalls {
		if err := call(); err == nil {
			t.Fatalf("call %d unexpectedly succeeded", i)
		}
		if got := snapshotState(r); !reflect.DeepEqual(got, before) {
			t.Fatalf("call %d changed state: %#v", i, got)
		}
	}
}

func TestVisitedIndependentOfCommittedHistory(t *testing.T) {
	measure := func(checkpoints int) int {
		r, _ := New(1, 1000)
		for cp := 1; cp <= checkpoints+1; cp++ {
			mustWrite(t, r, 0, 1)
			mustBarrier(t, r, cp, []Name{name(0, cp)})
		}
		committedCheckpoints := make([]Name, checkpoints)
		for cp := 1; cp <= checkpoints; cp++ {
			committedCheckpoints[cp-1] = name(0, cp)
		}
		mustComplete(t, r, checkpoints, committedCheckpoints)
		before := r.visited
		mustComplete(t, r, checkpoints+1, []Name{name(0, checkpoints+1)})
		return r.visited - before
	}

	if got10, got100000 := measure(10), measure(100000); got10 != 1 || got100000 != 1 {
		t.Fatalf("visited deltas = %d and %d", got10, got100000)
	}
}

func TestConcurrentOperations(t *testing.T) {
	r, _ := New(4, 2)
	stop := make(chan struct{})
	var wg sync.WaitGroup

	wg.Add(4)
	for reader := 0; reader < 4; reader++ {
		go func(id int) {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_, _ = r.Txn(id%4, 1)
					_ = r.Pending()
					_ = r.Stats()
				}
			}
		}(reader)
	}

	for checkpoint := 1; checkpoint <= 20; checkpoint++ {
		if err := r.Write((checkpoint-1)%4, checkpoint); err != nil {
			t.Fatal(err)
		}
		if _, err := r.Barrier(checkpoint); err != nil {
			t.Fatal(err)
		}
		if checkpoint%4 == 0 {
			if _, err := r.Complete(checkpoint); err != nil {
				t.Fatal(err)
			}
		}
		if checkpoint == 10 {
			if _, err := r.Restore(10, 4); err != nil {
				t.Fatal(err)
			}
		}
	}

	close(stop)
	wg.Wait()

	stats := r.Stats()
	if total := stats.CommittedRecords + stats.AbortedRecords + stats.OpenRecords + stats.PreparedRecords; total <= 0 {
		t.Fatalf("unexpected conservation total from concurrent readers: %#v", stats)
	}
}
