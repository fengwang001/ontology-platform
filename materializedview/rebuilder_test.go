package materializedview

import (
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
)

func testEvents() []Event {
	return []Event{
		{Key: "a"}, {Key: "b"}, {Key: "a"}, {Key: "c"},
		{Key: "b"}, {Key: "a"}, {Key: "c"},
	}
}

func logProgress(t *testing.T, operation string, progress RebuildProgress, snapshot ViewSnapshot) {
	t.Helper()
	t.Logf(
		"op=%s processed=%d shadow=%v view=%v generation=%d active=%t recoverable=%t complete=%t reason=operation returns a committed snapshot and checkpoint metadata",
		operation,
		progress.Processed,
		progress.Shadow,
		snapshot.View,
		snapshot.Generation,
		progress.Active,
		progress.Recoverable,
		progress.Complete,
	)
}

func TestInvalidOperationsDoNotChangeState(t *testing.T) {
	oldView := CountView{"old": 1}
	events := testEvents()

	assertSnapshot := func(name string, r *Rebuilder, want ViewSnapshot) {
		t.Helper()
		got := r.Snapshot()
		if got.Generation != want.Generation || !reflect.DeepEqual(got.View, want.View) {
			t.Fatalf("%s changed committed state: got %+v want %+v", name, got, want)
		}
	}

	t.Run("invalid block size is rejected", func(t *testing.T) {
		r := NewRebuilder(events, oldView, 3)
		before := r.Progress()
		if err := r.BeginRebuild(0); !errors.Is(err, ErrInvalidBlockSize) {
			t.Fatalf("BeginRebuild error = %v, want %v", err, ErrInvalidBlockSize)
		}
		after := r.Progress()
		if !reflect.DeepEqual(before, after) {
			t.Fatalf("failed BeginRebuild changed progress: before=%+v after=%+v", before, after)
		}
		assertSnapshot(t.Name(), r, ViewSnapshot{Generation: 3, View: oldView})
	})

	t.Run("begin while active is rejected", func(t *testing.T) {
		r := NewRebuilder(events, oldView, 3)
		if err := r.BeginRebuild(2); err != nil {
			t.Fatal(err)
		}
		if _, err := r.StepRebuild(); err != nil {
			t.Fatal(err)
		}
		before := r.Progress()
		if err := r.BeginRebuild(4); !errors.Is(err, ErrRebuildAlreadyOpen) {
			t.Fatalf("second BeginRebuild error = %v, want %v", err, ErrRebuildAlreadyOpen)
		}
		after := r.Progress()
		if !reflect.DeepEqual(before, after) {
			t.Fatalf("failed BeginRebuild changed progress: before=%+v after=%+v", before, after)
		}
	})

	t.Run("step commit and crash without rebuild are rejected", func(t *testing.T) {
		r := NewRebuilder(events, oldView, 3)
		before := r.Progress()

		if _, err := r.StepRebuild(); !errors.Is(err, ErrNoRebuild) {
			t.Fatalf("StepRebuild error = %v, want %v", err, ErrNoRebuild)
		}
		if err := r.CommitRebuild(); !errors.Is(err, ErrNoRebuild) {
			t.Fatalf("CommitRebuild error = %v, want %v", err, ErrNoRebuild)
		}
		if err := r.CrashRebuild(); !errors.Is(err, ErrNoRebuild) {
			t.Fatalf("CrashRebuild error = %v, want %v", err, ErrNoRebuild)
		}

		if !reflect.DeepEqual(before, r.Progress()) {
			t.Fatalf("operation without rebuild changed progress: before=%+v after=%+v", before, r.Progress())
		}
		assertSnapshot(t.Name(), r, ViewSnapshot{Generation: 3, View: oldView})
	})

	t.Run("incomplete commit is rejected", func(t *testing.T) {
		r := NewRebuilder(events, oldView, 3)
		if err := r.BeginRebuild(2); err != nil {
			t.Fatal(err)
		}
		progress, err := r.StepRebuild()
		if err != nil {
			t.Fatal(err)
		}
		logProgress(t, "step-before-rejected-commit", progress, r.Snapshot())

		err = r.CommitRebuild()
		if !errors.Is(err, ErrRebuildIncomplete) {
			t.Fatalf("incomplete CommitRebuild error = %v, want %v", err, ErrRebuildIncomplete)
		}

		after := r.Progress()
		if !after.Active || after.Processed != 2 || !reflect.DeepEqual(after.Shadow, CountView{"a": 1, "b": 1}) {
			t.Fatalf("rejected commit changed rebuild state: %+v", after)
		}
		assertSnapshot(t.Name(), r, ViewSnapshot{Generation: 3, View: oldView})
	})
}

func TestCrashResumesFromLastCheckpoint(t *testing.T) {
	oldView := CountView{"old": 1}
	r := NewRebuilder(testEvents(), oldView, 7)

	if err := r.BeginRebuild(2); err != nil {
		t.Fatal(err)
	}
	first, err := r.StepRebuild()
	if err != nil {
		t.Fatal(err)
	}
	logProgress(t, "checkpoint-block-1", first, r.Snapshot())
	second, err := r.StepRebuild()
	if err != nil {
		t.Fatal(err)
	}
	logProgress(t, "checkpoint-block-2", second, r.Snapshot())

	if err := r.CrashRebuild(); err != nil {
		t.Fatal(err)
	}
	crashed := r.Progress()
	logProgress(t, "crash-between-blocks", crashed, r.Snapshot())
	if crashed.Active || !crashed.Recoverable || crashed.Processed != 4 {
		t.Fatalf("checkpoint after crash = %+v", crashed)
	}
	if !reflect.DeepEqual(r.View(), oldView) || r.Generation() != 7 {
		t.Fatal("old view changed during crash")
	}

	if err := r.BeginRebuild(99); err != nil {
		t.Fatal(err)
	}
	resumed, err := r.StepRebuild()
	if err != nil {
		t.Fatal(err)
	}
	logProgress(t, "resume-from-checkpoint", resumed, r.Snapshot())
	if resumed.Processed != 6 || resumed.BlockSize != 2 || !reflect.DeepEqual(resumed.Shadow, CountView{"a": 3, "b": 2, "c": 1}) {
		t.Fatalf("resumed progress = %+v", resumed)
	}

	if err := r.CrashRebuild(); err != nil {
		t.Fatal(err)
	}
	if err := r.BeginRebuild(2); err != nil {
		t.Fatal(err)
	}
	if err := r.CommitRebuild(); !errors.Is(err, ErrRebuildIncomplete) {
		t.Fatalf("CommitRebuild before completion = %v, want %v", err, ErrRebuildIncomplete)
	}
	last, err := r.StepRebuild()
	if err != nil {
		t.Fatal(err)
	}
	logProgress(t, "final-checkpoint", last, r.Snapshot())

	if !reflect.DeepEqual(r.View(), oldView) || r.Generation() != 7 {
		t.Fatal("old view changed before commit")
	}
	if err := r.CommitRebuild(); err != nil {
		t.Fatal(err)
	}
	committed := r.Snapshot()
	logProgress(t, "atomic-commit", r.Progress(), committed)
	if committed.Generation != 8 || !reflect.DeepEqual(committed.View, CountView{"a": 3, "b": 2, "c": 2}) {
		t.Fatalf("committed snapshot = %+v", committed)
	}
}

func TestOldViewRemainsReadableUntilAtomicSwitch(t *testing.T) {
	oldView := CountView{"old": 4}
	r := NewRebuilder(testEvents(), oldView, 11)
	if err := r.BeginRebuild(3); err != nil {
		t.Fatal(err)
	}

	for i := 1; i <= 3; i++ {
		progress, err := r.StepRebuild()
		if err != nil {
			t.Fatal(err)
		}
		snapshot := r.Snapshot()
		logProgress(t, "read-old-view-during-rebuild", progress, snapshot)
		if snapshot.Generation != 11 || !reflect.DeepEqual(snapshot.View, oldView) {
			t.Fatalf("during rebuild read %+v, want old committed view", snapshot)
		}
	}

	if !r.Progress().Complete {
		t.Fatal("rebuild should be complete but not committed")
	}
	beforeCommit := r.Snapshot()
	if beforeCommit.Generation != 11 || !reflect.DeepEqual(beforeCommit.View, oldView) {
		t.Fatalf("completed shadow is visible before commit: %+v", beforeCommit)
	}

	if err := r.CommitRebuild(); err != nil {
		t.Fatal(err)
	}
	afterCommit := r.Snapshot()
	logProgress(t, "read-new-view-after-switch", r.Progress(), afterCommit)
	if afterCommit.Generation != 12 || !reflect.DeepEqual(afterCommit.View, ReplaySource(testEvents())) {
		t.Fatalf("after switch read %+v, want new committed view", afterCommit)
	}
}

func TestRebuildMatchesReplay(t *testing.T) {
	events := testEvents()
	want := ReplaySource(events)
	r := NewRebuilder(events, CountView{"stale": 99}, 1)

	if err := r.BeginRebuild(3); err != nil {
		t.Fatal(err)
	}
	for !r.Progress().Complete {
		progress, err := r.StepRebuild()
		if err != nil {
			t.Fatal(err)
		}
		logProgress(t, "chunked-replay-checkpoint", progress, r.Snapshot())
	}
	if err := r.CommitRebuild(); err != nil {
		t.Fatal(err)
	}

	snapshot := r.Snapshot()
	logProgress(t, "compare-with-replay-from-zero", r.Progress(), snapshot)
	if !reflect.DeepEqual(snapshot.View, want) {
		t.Fatalf("chunked rebuild = %v, want from-scratch replay %v", snapshot.View, want)
	}

	r2 := NewRebuilder(events, snapshot.View, snapshot.Generation)
	if err := r2.BeginRebuild(2); err != nil {
		t.Fatal(err)
	}
	for !r2.Progress().Complete {
		if _, err := r2.StepRebuild(); err != nil {
			t.Fatal(err)
		}
	}
	if err := r2.CommitRebuild(); err != nil {
		t.Fatal(err)
	}
	second := r2.Snapshot()
	if second.Generation != 3 || !reflect.DeepEqual(second.View, want) {
		t.Fatalf("repeat rebuild = %+v, want generation 3 and same view", second)
	}
}

func TestConcurrentReadsOnlySeeCommittedBoundaries(t *testing.T) {
	oldView := CountView{"old": 5}
	newView := ReplaySource(testEvents())
	r := NewRebuilder(testEvents(), oldView, 20)
	if err := r.BeginRebuild(2); err != nil {
		t.Fatal(err)
	}

	stop := make(chan struct{})
	ready := make(chan struct{}, 16)
	observed := make(chan struct{}, 16)
	barrier := make(chan struct{})
	var wg sync.WaitGroup
	var reads atomic.Int64

	read := func() (ViewSnapshot, bool) {
		snapshot := r.Snapshot()
		reads.Add(1)
		select {
		case observed <- struct{}{}:
		default:
		}

		switch snapshot.Generation {
		case 20:
			return snapshot, reflect.DeepEqual(snapshot.View, oldView)
		case 21:
			return snapshot, reflect.DeepEqual(snapshot.View, newView)
		default:
			return snapshot, false
		}
	}

	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ready <- struct{}{}
			<-barrier
			for {
				select {
				case <-stop:
					return
				default:
					if snapshot, ok := read(); !ok {
						t.Errorf("read uncommitted boundary: gen=%d view=%v", snapshot.Generation, snapshot.View)
						return
					}

					generation := r.Generation()
					view := r.View()
					_ = r.Progress()
					if generation != 20 && generation != 21 {
						t.Errorf("unexpected generation: %d", generation)
					}
					if !reflect.DeepEqual(view, oldView) && !reflect.DeepEqual(view, newView) {
						t.Errorf("view was neither committed boundary: %v", view)
					}
				}
			}
		}()
	}
	for i := 0; i < 16; i++ {
		<-ready
	}
	close(barrier)
	for i := 0; i < 16; i++ {
		<-observed
	}

	for !r.Progress().Complete {
		progress, err := r.StepRebuild()
		if err != nil {
			t.Fatal(err)
		}
		logProgress(t, "concurrent-read-during-checkpoint", progress, r.Snapshot())
	}

	if err := r.CommitRebuild(); err != nil {
		t.Fatal(err)
	}
	close(stop)
	wg.Wait()

	final := r.Snapshot()
	logProgress(t, "concurrent-read-final", r.Progress(), final)
	if final.Generation != 21 || !reflect.DeepEqual(final.View, newView) {
		t.Fatalf("final snapshot = %+v", final)
	}
	if reads.Load() == 0 {
		t.Fatal("no concurrent reads were observed")
	}
}
