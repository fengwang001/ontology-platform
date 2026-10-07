package store

import (
	"context"
	"errors"
	"testing"
)

// TestRecoveryDeterministic recovers the same crashed disk twice from
// independent clones and requires identical classification. Repeated
// recovery must not flip the verdict.
func TestRecoveryDeterministic(t *testing.T) {
	ids := []string{"d-1", "d-2", "d-3"}
	for _, crash := range []string{
		barrierActivePointer,
		barrierStatePrepared,
		barrierStateCommitted,
		barrierInstanceApply + ".1",
		barrierStateDone + ".commit",
	} {
		t.Run(crash, func(t *testing.T) {
			disk := NewMemDisk()
			seedData(t, disk, ids...)
			var seen []string
			st, _ := openOn(t, disk, crashOnceAt(crash, &seen))
			_, err := st.Batch(context.Background(), "det", sampleOps(ids))
			var fatal *FatalCrashError
			if !errors.As(err, &fatal) {
				t.Fatalf("crash: %v", err)
			}

			cloneA := disk.Clone()
			cloneB := disk.Clone()
			_, repA, err := Open(context.Background(), NewMemEngine(cloneA))
			if err != nil {
				t.Fatal(err)
			}
			_, repB, err := Open(context.Background(), NewMemEngine(cloneB))
			if err != nil {
				t.Fatal(err)
			}
			classA, classB := classOf(repA), classOf(repB)
			if classA != classB {
				t.Fatalf("non-deterministic classification: %s vs %s", classA, classB)
			}

			// Recovering cloneA a second time must not change class or
			// re-apply anything.
			stA2, repA2, err := Open(context.Background(), NewMemEngine(cloneA))
			if err != nil {
				t.Fatal(err)
			}
			if classOf(repA2) != "" && classOf(repA2) != classA {
				t.Fatalf("second reopen class changed: %s", classOf(repA2))
			}
			_ = stA2
			assertModel(t, stA2, ids, sampleOps(ids),
				classA == ClassRecoveredCommit)
		})
	}
}

func classOf(reps []RecoveryReport) Class {
	if len(reps) == 0 {
		return ""
	}
	return reps[0].Class
}
