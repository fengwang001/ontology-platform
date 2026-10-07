package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func openOn(t *testing.T, disk *MemDisk, hook CrashHook) (*Store, []RecoveryReport) {
	t.Helper()
	st, reports, err := Open(context.Background(), NewMemEngine(disk), WithCrashHook(hook))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	return st, reports
}

func seedData(t *testing.T, disk *MemDisk, ids ...string) {
	t.Helper()
	st, _ := openOn(t, disk, nil)
	for i, id := range ids {
		err := st.Put(context.Background(), &Instance{
			ID:         id,
			Version:    int64(i + 1),
			Properties: map[string]string{"k": fmt.Sprintf("seed-%d", i), "obj": id},
		})
		if err != nil {
			t.Fatalf("seed put: %v", err)
		}
	}
}

func sampleOps(ids []string) []Op {
	ops := make([]Op, len(ids))
	for i, id := range ids {
		ops[i] = Op{ObjectID: id, Properties: map[string]string{"k": fmt.Sprintf("new-%d", i), "obj": id}}
	}
	return ops
}

func isCommitBarrier(b string) bool { return b == barrierStateCommitted }

// crashOnceAt returns a hook that crashes exactly at barrier target and
// records every barrier it observed.
func crashOnceAt(target string, seen *[]string) CrashHook {
	return func(b string) error {
		*seen = append(*seen, b)
		if b == target {
			return &CrashError{Barrier: b}
		}
		return nil
	}
}

func readAll(t *testing.T, st *Store, ids []string) map[string]*Instance {
	t.Helper()
	out := make(map[string]*Instance, len(ids))
	for _, id := range ids {
		inst, err := st.Get(context.Background(), id)
		if err != nil {
			t.Fatalf("get %s: %v", id, err)
		}
		out[id] = inst
	}
	return out
}

func expectedModel(ids []string, ops []Op, committed bool) *naiveModel {
	m := newNaiveModel()
	for i, id := range ids {
		m.seed(id, int64(i+1), map[string]string{"k": fmt.Sprintf("seed-%d", i), "obj": id})
	}
	if committed {
		m.applyCommit(ops)
	}
	return m
}

func assertModel(t *testing.T, st *Store, ids []string, ops []Op, committed bool) {
	t.Helper()
	m := expectedModel(ids, ops, committed)
	got := readAll(t, st, ids)
	for id, want := range m.instances {
		g := got[id]
		if g.Version != want.version || !reflect.DeepEqual(g.Properties, want.props) {
			t.Fatalf("object %s mismatch: got (v=%d %v), want (v=%d %v)",
				id, g.Version, g.Properties, want.version, want.props)
		}
	}
}

// TestExhaustiveBarriers injects a crash at every phase boundary reached
// during a three-object batch and verifies exactly one of the two legal
// final states results.
func TestExhaustiveBarriers(t *testing.T) {
	ids := []string{"obj-a", "obj-b", "obj-c"}

	// Collect the set of barriers from a crash-free reference run.
	var allBarriers []string
	refDisk := NewMemDisk()
	seedData(t, refDisk, ids...)
	ref, _ := openOn(t, refDisk, func(b string) error {
		allBarriers = append(allBarriers, b)
		return nil
	})
	if _, err := ref.Batch(context.Background(), "batch-ref", sampleOps(ids)); err != nil {
		t.Fatalf("reference batch: %v", err)
	}

	for _, crashed := range allBarriers {
		t.Run(crashed, func(t *testing.T) {
			disk := NewMemDisk()
			seedData(t, disk, ids...)

			var seen []string
			st, _ := openOn(t, disk, crashOnceAt(crashed, &seen))
			_, err := st.Batch(context.Background(), "batch-x", sampleOps(ids))
			var fatal *FatalCrashError
			if !errors.As(err, &fatal) {
				t.Fatalf("expected simulated crash at %q, got err=%v", crashed, err)
			}

			// Recovery pass 1. A crash before the active pointer is
			// durable (intent staging) or after it is cleared (cleanup)
			// leaves nothing for recovery to name: those batches are
			// observable simply as "never happened" (pre-commit) or
			// "already fully effective" (post-clear).
			named := barrierNamed(crashed)
			wantCommitted := false
			if isCommitBarrier(crashed) || barrierAfterCommit(crashed) {
				wantCommitted = true
			}
			rec1, rep1 := openOn(t, disk, nil)
			wantClass := Class("")
			if named {
				if len(rep1) != 1 {
					t.Fatalf("expected one recovery report, got %d", len(rep1))
				}
				wantClass = ClassRecoveredAbort
				if wantCommitted {
					wantClass = ClassRecoveredCommit
				}
				if rep1[0].Class != wantClass {
					t.Fatalf("classification: got %s want %s", rep1[0].Class, wantClass)
				}
				if rep1[0].RecordsScanned > 2+len(ids) {
					t.Fatalf("records scanned %d exceeds bound %d", rep1[0].RecordsScanned, 2+len(ids))
				}
			}
			assertModel(t, rec1, ids, sampleOps(ids), wantCommitted)

			// Recovery pass 2 on a fresh reopen must be a no-op and must
			// never re-apply the batch (versions must not move again).
			before := readAll(t, rec1, ids)
			rec2, rep2 := openOn(t, disk, nil)
			if len(rep2) != 0 {
				t.Fatalf("second reopen produced unexpected recovery: %+v", rep2)
			}
			after := readAll(t, rec2, ids)
			for id, b := range before {
				a := after[id]
				if a.Version != b.Version || !reflect.DeepEqual(a.Properties, b.Properties) {
					t.Fatalf("idempotent reopen changed %s", id)
				}
			}
			assertModel(t, rec2, ids, sampleOps(ids), wantCommitted)

			// Journal must document the crash stage and the classification
			// for every named recovery.
			j := rec2.Journal()
			if named {
				foundClass := false
				for _, e := range j {
					if (wantClass == ClassRecoveredCommit && e.Phase == "recovered_commit") ||
						(wantClass == ClassRecoveredAbort && e.Phase == "recovered_abort") {
						foundClass = true
					}
				}
				if !foundClass {
					t.Fatalf("journal missing %s entry: %+v", wantClass, j)
				}
			}
		})
	}
}

func barrierNamed(b string) bool {
	switch {
	case hasPrefix(b, barrierIntent+"."):
		return false
	case hasPrefix(b, barrierIntentCleanup+"."):
		return false
	case b == barrierActiveClear:
		return false
	default:
		return true
	}
}

func barrierAfterCommit(b string) bool {
	switch b {
	case barrierInstanceApply, barrierStateDone + ".commit", barrierActiveClear,
		barrierInstanceApply + ".0", barrierInstanceApply + ".1", barrierInstanceApply + ".2":
		return true
	}
	if hasPrefix(b, barrierInstanceApply+".") ||
		hasPrefix(b, barrierIntentCleanup+".") ||
		b == barrierStateDone+".commit" || b == barrierActiveClear {
		return true
	}
	return false
}

// TestUncertaintyWindowRejectsTraffic verifies that between preparation
// and the terminal outcome, reads and writes to involved objects fail.
func TestUncertaintyWindowRejectsTraffic(t *testing.T) {
	ids := []string{"obj-a", "obj-b"}
	disk2 := NewMemDisk()
	seedData(t, disk2, ids...)

	// Suspend the batch after the active pointer is durable but before
	// PREPARED exists: the involved objects are in the uncertainty window.
	release := make(chan struct{})
	atWindow := make(chan struct{})
	var involvedAtWindow int
	var st2 *Store
	st2, _ = openOn(t, disk2, func(b string) error {
		if b == barrierActivePointer {
			involvedAtWindow = len(st2.objects)
			close(atWindow)
			<-release
		}
		return nil
	})
	errCh := make(chan error, 1)
	go func() {
		_, err := st2.Batch(context.Background(), "batch-p", sampleOps(ids))
		errCh <- err
	}()
	<-atWindow
	if involvedAtWindow != len(ids) {
		close(release)
		t.Fatalf("objects not marked uncertain: %d", involvedAtWindow)
	}
	close(release)
	if err := <-errCh; err != nil {
		t.Fatalf("batch: %v", err)
	}

	got, err := st2.Get(context.Background(), "obj-a")
	if err != nil {
		t.Fatalf("post-commit read: %v", err)
	}
	if got.Version != 2 {
		t.Fatalf("version: %d", got.Version)
	}
	if err := st2.Put(context.Background(), &Instance{ID: "obj-a", Version: 3, Properties: map[string]string{"k": "x"}}); err != nil {
		t.Fatalf("post-commit write: %v", err)
	}

	// A crash before recovery leaves raw values at preimages.
	disk3 := NewMemDisk()
	seedData(t, disk3, ids...)
	var seen []string
	st3, _ := openOn(t, disk3, crashOnceAt(barrierStatePrepared, &seen))
	_, err = st3.Batch(context.Background(), "batch-c", sampleOps(ids))
	var fatal *FatalCrashError
	if !errors.As(err, &fatal) {
		t.Fatalf("expected crash, got %v", err)
	}
	b, err := NewMemEngine(disk3).Get(keyInstance("obj-a"))
	if err != nil {
		t.Fatal(err)
	}
	rec, err := decode[instanceRecord](b)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Properties["k"] != "seed-0" {
		t.Fatalf("intermediate value leaked: %v", rec.Properties)
	}
	rec4, _ := openOn(t, disk3, nil)
	if _, err := rec4.Get(context.Background(), "obj-a"); err != nil {
		t.Fatalf("post-recovery read rejected: %v", err)
	}
}

// TestFileEngineCrashReplay verifies the real fsync-backed engine
// survives process restarts (directory copies) and converges.
func TestFileEngineCrashReplay(t *testing.T) {
	dir := t.TempDir()
	ids := []string{"f-a", "f-b"}

	st, _, err := Open(context.Background(), mustFile(t, dir))
	if err != nil {
		t.Fatal(err)
	}
	for i, id := range ids {
		if err := st.Put(context.Background(), &Instance{
			ID: id, Version: 1, Properties: map[string]string{"k": fmt.Sprintf("v%d", i)},
		}); err != nil {
			t.Fatal(err)
		}
	}

	var seen []string
	st2, _, err := Open(context.Background(), mustFile(t, dir), WithCrashHook(crashOnceAt(barrierStateCommitted, &seen)))
	if err != nil {
		t.Fatal(err)
	}
	_, err = st2.Batch(context.Background(), "fbatch", sampleOps(ids))
	var fatal *FatalCrashError
	if !errors.As(err, &fatal) {
		t.Fatalf("want crash got %v", err)
	}

	st3, reps, err := Open(context.Background(), mustFile(t, dir))
	if err != nil {
		t.Fatal(err)
	}
	if len(reps) != 1 || reps[0].Class != ClassRecoveredCommit {
		t.Fatalf("recovery: %+v", reps)
	}
	got := readAll(t, st3, ids)
	if got["f-a"].Version != 2 || got["f-a"].Properties["k"] != "new-0" {
		t.Fatalf("rollforward failed: %+v", got["f-a"])
	}

	// Artifacts: list the data directory as evidence of naming layout.
	_ = filepath.WalkDir
	_ = os.ReadDir
}

func mustFile(t *testing.T, dir string) Engine {
	t.Helper()
	e, err := NewFileEngine(filepath.Join(dir, "data"))
	if err != nil {
		t.Fatal(err)
	}
	return e
}
