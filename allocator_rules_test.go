package ontology

import (
	"errors"
	"reflect"
	"testing"
)

func TestPreferenceAndStealing(t *testing.T) {
	a := newTestAllocator(t, 3, 2)
	addSplits(t, a, 0, 3, 6)

	if got := mustRequest(t, a, 0); got.Split != 0 {
		t.Fatalf("preferred split = %d, want 0", got.Split)
	}
	if got := mustRequest(t, a, 1); got.Kind != RequestWaiting {
		t.Fatalf("no failed owner result = %v, want waiting", got.Kind)
	}
	if _, err := a.ReaderFailed(0); err != nil {
		t.Fatal(err)
	}
	if got := mustRequest(t, a, 1); got.Kind != RequestAssigned || got.Split != 0 {
		t.Fatalf("stolen split = %+v, want 0", got)
	}
	if got := mustRequest(t, a, 1); got.Kind != RequestAssigned || got.Split != 3 {
		t.Fatalf("stolen split = %+v, want 3", got)
	}
}

func TestRetainedSplitsAreNotStolen(t *testing.T) {
	a := newTestAllocator(t, 2, 2)
	addSplits(t, a, 0)
	if got := mustRequest(t, a, 0); got.Split != 0 {
		t.Fatalf("split = %d, want 0", got.Split)
	}
	if err := a.Checkpoint(1); err != nil {
		t.Fatal(err)
	}
	if err := a.Complete(1); err != nil {
		t.Fatal(err)
	}
	failed, err := a.ReaderFailed(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(failed.Returned) != 0 || len(failed.Revoked) != 0 {
		t.Fatalf("failed result = %+v, want retained split", failed)
	}
	if got := mustRequest(t, a, 1); got.Kind != RequestWaiting {
		t.Fatalf("request = %+v, want waiting for retained split", got)
	}
	a.Seal()
	if got := mustRequest(t, a, 1); got.Kind != RequestWaiting {
		t.Fatalf("sealed request = %+v, want waiting for retained split", got)
	}
	assertState(t, a, 0, Assigned, 0, 0)
}

func TestFinishedAfterLastCompletedIsReturned(t *testing.T) {
	a := newTestAllocator(t, 2, 2)
	addSplits(t, a, 0)
	if got := mustRequest(t, a, 0); got.Split != 0 {
		t.Fatalf("split = %d, want 0", got.Split)
	}
	if err := a.Checkpoint(1); err != nil {
		t.Fatal(err)
	}
	if err := a.Finished(0, 0); err != nil {
		t.Fatal(err)
	}
	failed, err := a.ReaderFailed(0)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(failed.Returned, []int64{0}) || len(failed.Quarantined) != 0 {
		t.Fatalf("failed = %+v, want returned finished split", failed)
	}
	assertState(t, a, 0, Unassigned, -1, 1)
}

func TestQuarantineThresholdAndDuplicateRejected(t *testing.T) {
	a := newTestAllocator(t, 1, 2)
	addSplits(t, a, 0)

	for attempt, wantState := range []SplitState{Unassigned, Quarantined} {
		if got := mustRequest(t, a, 0); got.Split != 0 {
			t.Fatalf("attempt %d split = %d, want 0", attempt, got.Split)
		}
		failed, err := a.ReaderFailed(0)
		if err != nil {
			t.Fatal(err)
		}
		if attempt == 0 {
			if len(failed.Quarantined) != 0 {
				t.Fatalf("attempt 0 quarantined = %v, want empty", failed.Quarantined)
			}
			if err := a.ReaderRestarted(0); err != nil {
				t.Fatal(err)
			}
		} else if !reflect.DeepEqual(failed.Quarantined, []int64{0}) {
			t.Fatalf("attempt 1 quarantined = %v, want [0]", failed.Quarantined)
		}
		assertState(t, a, 0, wantState, ownerForState(wantState), attempt+1)
	}

	if got := a.Quarantined(); !reflect.DeepEqual(got, []int64{0}) {
		t.Fatalf("quarantined = %v, want [0]", got)
	}
	if err := a.AddSplits([]int64{0}); !errors.Is(err, ErrDuplicateSplit) {
		t.Fatalf("duplicate quarantined split error = %v, want %v", err, ErrDuplicateSplit)
	}
	assertState(t, a, 0, Quarantined, -1, 2)
}

func TestNoMoreSplitsRequiresSealedAndNoAssignments(t *testing.T) {
	a := newTestAllocator(t, 1, 2)
	addSplits(t, a, 0)
	if got := mustRequest(t, a, 0); got.Kind != RequestAssigned {
		t.Fatalf("request = %+v, want assigned", got)
	}
	if err := a.Finished(0, 0); err != nil {
		t.Fatal(err)
	}
	a.Seal()
	if got := mustRequest(t, a, 0); got.Kind != RequestNoMoreSplits {
		t.Fatalf("finished only request = %+v, want no more splits", got)
	}
}

func TestCompleteAheadAndStale(t *testing.T) {
	a := newTestAllocator(t, 1, 2)
	if err := a.Complete(1); !errors.Is(err, ErrCheckpointAhead) {
		t.Fatalf("ahead error = %v, want %v", err, ErrCheckpointAhead)
	}
	if err := a.Checkpoint(1); err != nil {
		t.Fatal(err)
	}
	if err := a.Complete(1); err != nil {
		t.Fatal(err)
	}
	if err := a.Complete(1); !errors.Is(err, ErrCheckpointStale) {
		t.Fatalf("stale error = %v, want %v", err, ErrCheckpointStale)
	}
}

func ownerForState(state SplitState) int {
	if state == Quarantined || state == Unassigned {
		return -1
	}
	return 0
}
