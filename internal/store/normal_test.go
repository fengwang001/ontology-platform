package store

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func TestNormalCommit(t *testing.T) {
	disk := NewMemDisk()
	seedData(t, disk, "a", "b", "c")
	st, reps := openOn(t, disk, nil)
	if len(reps) != 0 {
		t.Fatalf("unexpected recovery: %+v", reps)
	}
	out, err := st.Batch(context.Background(), "b1", []Op{
		{ObjectID: "a", Properties: map[string]string{"v": "1"}},
		{ObjectID: "c", Properties: map[string]string{"v": "2"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Class != ClassCommitted {
		t.Fatalf("class=%s", out.Class)
	}
	a, _ := st.Get(context.Background(), "a")
	b, _ := st.Get(context.Background(), "b")
	c, _ := st.Get(context.Background(), "c")
	if a.Version != 2 || a.Properties["v"] != "1" {
		t.Fatalf("a=%+v", a)
	}
	if b.Version != 2 { // seeded as second id -> version 2, unchanged by batch
		t.Fatalf("b=%+v", b)
	}
	if c.Version != 4 || c.Properties["v"] != "2" {
		t.Fatalf("c=%+v", c)
	}

	// New object creation inside a batch.
	_, err = st.Batch(context.Background(), "b2", []Op{
		{ObjectID: "new", Properties: map[string]string{"v": "z"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	n, err := st.Get(context.Background(), "new")
	if err != nil {
		t.Fatal(err)
	}
	if n.Version != 1 || n.Properties["v"] != "z" {
		t.Fatalf("new=%+v", n)
	}

	// Two batches must not run concurrently.
	if _, err := st.Batch(context.Background(), "b3", nil); err == nil {
		t.Fatal("empty batch accepted")
	}
}

func TestNormalAbort(t *testing.T) {
	ids := []string{"a", "b"}
	disk2 := NewMemDisk()
	seedData(t, disk2, ids...)
	st2, _ := openOn(t, disk2, nil)
	out, err := st2.AbortPrepared(context.Background(), "bb", sampleOps(ids))
	if err != nil {
		t.Fatal(err)
	}
	if out.Class != ClassAborted {
		t.Fatalf("class=%s", out.Class)
	}
	assertModel(t, st2, ids, sampleOps(ids), false)
	// Journal records a normal (non-recovered) abort.
	last := st2.Journal()[len(st2.Journal())-1]
	if last.Phase != "aborted" {
		t.Fatalf("journal=%+v", last)
	}
}

// TestRecoveryInterruptedDuringRecovery crashes inside the first recovery
// at every barrier and then recovers again, repeatedly, asserting
// convergence to the same single terminal state.
func TestRecoveryInterruptedDuringRecovery(t *testing.T) {
	ids := []string{"r-a", "r-b", "r-c", "r-d"}

	// Barriers observable during recovery of a COMMITTED crash.
	recBarriers := []string{
		barrierInstanceApply + ".0",
		barrierInstanceApply + ".1",
		barrierInstanceApply + ".2",
		barrierInstanceApply + ".3",
		barrierStateDone + ".commit",
		barrierActiveClear,
		barrierIntentCleanup + ".0",
		barrierIntentCleanup + ".3",
	}

	for _, secondCrash := range recBarriers {
		t.Run("commit/"+secondCrash, func(t *testing.T) {
			disk := NewMemDisk()
			seedData(t, disk, ids...)

			var s1 []string
			st1, _ := openOn(t, disk, crashOnceAt(barrierStateCommitted, &s1))
			_, err := st1.Batch(context.Background(), "bx", sampleOps(ids))
			var fatal *FatalCrashError
			if !errors.As(err, &fatal) {
				t.Fatalf("first crash: %v", err)
			}

			// Recovery #1 crashes at secondCrash.
			var s2 []string
			func() {
				defer func() { _ = recover() }()
				rec, _, err := Open(context.Background(), NewMemEngine(disk),
					WithCrashHook(crashOnceAt(secondCrash, &s2)))
				if err == nil {
					_ = rec
				}
			}()

			// Recovery #2 clean: must converge to committed state. If the
			// second crash happened after the pointer was cleared, no
			// further recovery action is named (idempotent no-op).
			st3, reps, err := Open(context.Background(), NewMemEngine(disk))
			if err != nil {
				t.Fatal(err)
			}
			if secondCrash != barrierActiveClear && !hasPrefix(secondCrash, barrierIntentCleanup+".") {
				if len(reps) != 1 || reps[0].Class != ClassRecoveredCommit {
					t.Fatalf("final recovery: %+v err=%v", reps, err)
				}
			} else if len(reps) > 1 {
				t.Fatalf("unexpected reports: %+v", reps)
			}
			assertModel(t, st3, ids, sampleOps(ids), true)

			// Recovery #3: pure no-op, versions frozen.
			before := readAll(t, st3, ids)
			st4, reps4 := openOn(t, disk, nil)
			if len(reps4) != 0 {
				t.Fatalf("extra recovery: %+v", reps4)
			}
			after := readAll(t, st4, ids)
			for id := range before {
				if before[id].Version != after[id].Version {
					t.Fatalf("object %s version moved %d->%d", id, before[id].Version, after[id].Version)
				}
			}
		})
	}

	// Abort side: initial crash at PREPARED, then crash again during
	// recovery rollback at every reachable barrier.
	abortBarriers := []string{
		barrierStateDone + ".abort",
		barrierActiveClear,
		barrierIntentCleanup + ".0",
		barrierIntentCleanup + ".3",
	}
	for _, secondCrash := range abortBarriers {
		t.Run("abort/"+secondCrash, func(t *testing.T) {
			disk := NewMemDisk()
			seedData(t, disk, ids...)
			var s1 []string
			st1, _ := openOn(t, disk, crashOnceAt(barrierStatePrepared, &s1))
			_, err := st1.Batch(context.Background(), "by", sampleOps(ids))
			var fatal *FatalCrashError
			if !errors.As(err, &fatal) {
				t.Fatalf("first crash: %v", err)
			}
			var s2 []string
			_, _, _ = Open(context.Background(), NewMemEngine(disk),
				WithCrashHook(crashOnceAt(secondCrash, &s2)))

			st3, reps, err := Open(context.Background(), NewMemEngine(disk))
			if err != nil {
				t.Fatal(err)
			}
			if secondCrash != barrierActiveClear && !hasPrefix(secondCrash, barrierIntentCleanup+".") {
				if len(reps) != 1 || reps[0].Class != ClassRecoveredAbort {
					t.Fatalf("final recovery: %+v", reps)
				}
			}
			assertModel(t, st3, ids, sampleOps(ids), false)
			got := readAll(t, st3, ids)
			for i, id := range ids {
				if got[id].Version != int64(i+1) {
					t.Fatalf("%s version=%d", id, got[id].Version)
				}
			}
		})
	}
}

// TestBoundedRecoveryIndependentOfHistory seeds many completed batches and
// proves recovering one interrupted batch inspects only its own records.
func TestBoundedRecoveryIndependentOfHistory(t *testing.T) {
	disk := NewMemDisk()
	st, _ := openOn(t, disk, nil)
	const history = 50
	for i := 0; i < history; i++ {
		id := fmt.Sprintf("hist-%d", i)
		if err := st.Put(context.Background(), &Instance{ID: id, Version: 0, Properties: map[string]string{}}); err != nil {
			t.Fatal(err)
		}
		if _, err := st.Batch(context.Background(), fmt.Sprintf("old-%d", i), []Op{
			{ObjectID: id, Properties: map[string]string{"n": fmt.Sprint(i)}},
		}); err != nil {
			t.Fatal(err)
		}
	}

	// Interrupt a fresh 3-object batch at commit.
	ids := []string{"z-a", "z-b", "z-c"}
	seedData(t, disk, ids...)
	var seen []string
	st2, _ := openOn(t, disk, crashOnceAt(barrierStateCommitted, &seen))
	_, err := st2.Batch(context.Background(), "target", sampleOps(ids))
	var fatal *FatalCrashError
	if !errors.As(err, &fatal) {
		t.Fatal(err)
	}

	eng := NewMemEngine(disk)
	st3, reps, err := Open(context.Background(), eng)
	if err != nil {
		t.Fatal(err)
	}
	if len(reps) != 1 {
		t.Fatalf("reps=%+v", reps)
	}
	if reps[0].RecordsScanned != 2+len(ids) {
		t.Fatalf("scanned=%d want=%d (must not grow with %d historical batches)",
			reps[0].RecordsScanned, 2+len(ids), history)
	}
	assertModel(t, st3, ids, sampleOps(ids), true)
}
