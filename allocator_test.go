package ontology

import (
	"reflect"
	"testing"
)

func TestAllocatorExample(t *testing.T) {
	a, err := NewAllocator(2, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.AddSplits([]int64{0, 1, 2, 3, 4}); err != nil {
		t.Fatal(err)
	}
	if got := mustRequest(t, a, 0); got.Kind != RequestAssigned || got.Split != 0 {
		t.Fatalf("request = %+v, want assigned 0", got)
	}
	if got := mustRequest(t, a, 1); got.Kind != RequestAssigned || got.Split != 1 {
		t.Fatalf("request = %+v, want assigned 1", got)
	}
	if err := a.Checkpoint(1); err != nil {
		t.Fatal(err)
	}
	if got := mustRequest(t, a, 0); got.Split != 2 {
		t.Fatalf("split = %d, want 2", got.Split)
	}
	if err := a.Complete(1); err != nil {
		t.Fatal(err)
	}
	if err := a.Finished(0, 0); err != nil {
		t.Fatal(err)
	}
	if err := a.Checkpoint(2); err != nil {
		t.Fatal(err)
	}
	if got := mustRequest(t, a, 1); got.Split != 3 {
		t.Fatalf("split = %d, want 3", got.Split)
	}

	failed, err := a.ReaderFailed(0)
	if err != nil {
		t.Fatal(err)
	}
	wantFailed := ReaderFailedResult{
		Returned:    []int64{2},
		Quarantined: []int64{},
		Revoked:     []int64{0},
	}
	if !reflect.DeepEqual(failed, wantFailed) {
		t.Fatalf("failed = %+v, want %+v", failed, wantFailed)
	}

	if got := mustRequest(t, a, 1); got.Split != 2 {
		t.Fatalf("stolen split = %d, want 2", got.Split)
	}
	if got := mustRequest(t, a, 1); got.Split != 4 {
		t.Fatalf("stolen split = %d, want 4", got.Split)
	}
	if got := mustRequest(t, a, 1); got.Kind != RequestWaiting {
		t.Fatalf("kind = %v, want waiting", got.Kind)
	}
	a.Seal()
	if got := mustRequest(t, a, 1); got.Kind != RequestWaiting {
		t.Fatalf("kind after seal = %v, want waiting", got.Kind)
	}
	if err := a.Complete(2); err != nil {
		t.Fatal(err)
	}
	if _, err := a.RequestSplit(0); err != ErrReaderFailed {
		t.Fatalf("request after failed reader error = %v, want %v", err, ErrReaderFailed)
	}
}

func TestEpochBoundaries(t *testing.T) {
	a := newTestAllocator(t, 2, 100)
	addSplits(t, a, 0, 1, 2, 3, 4, 5)
	if got := mustRequest(t, a, 0); got.Split != 0 {
		t.Fatalf("split = %d, want 0", got.Split)
	}
	if got := mustRequest(t, a, 1); got.Split != 1 {
		t.Fatalf("split = %d, want 1", got.Split)
	}
	if got := mustRequest(t, a, 0); got.Split != 2 {
		t.Fatalf("split = %d, want 2", got.Split)
	}
	if got := mustRequest(t, a, 1); got.Split != 3 {
		t.Fatalf("split = %d, want 3", got.Split)
	}
	if err := a.Finished(1, 1); err != nil {
		t.Fatal(err)
	}
	if err := a.Checkpoint(1); err != nil {
		t.Fatal(err)
	}
	if err := a.Finished(0, 0); err != nil {
		t.Fatal(err)
	}
	if got := mustRequest(t, a, 0); got.Split != 4 {
		t.Fatalf("split = %d, want 4", got.Split)
	}
	if got := mustRequest(t, a, 1); got.Split != 5 {
		t.Fatalf("split = %d, want 5", got.Split)
	}
	if err := a.Complete(1); err != nil {
		t.Fatal(err)
	}

	failed, err := a.ReaderFailed(0)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(failed.Returned, []int64{4}) {
		t.Fatalf("returned = %v, want [4]", failed.Returned)
	}
	if !reflect.DeepEqual(failed.Revoked, []int64{0}) {
		t.Fatalf("revoked = %v, want [0]", failed.Revoked)
	}
	assertState(t, a, 0, Assigned, 0, 0)
	assertState(t, a, 4, Unassigned, -1, 1)

	failed, err = a.ReaderFailed(1)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(failed.Returned, []int64{5}) || len(failed.Revoked) != 0 {
		t.Fatalf("reader 1 failed = %+v, want returned [5] and no revoked", failed)
	}
	assertState(t, a, 1, Finished, 1, 0)
	assertState(t, a, 3, Assigned, 1, 0)
	assertState(t, a, 5, Unassigned, -1, 1)
}

func newTestAllocator(t *testing.T, readers int, quarantineAfter int) *Allocator {
	t.Helper()
	a, err := NewAllocator(readers, quarantineAfter)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func addSplits(t *testing.T, a *Allocator, splits ...int64) {
	t.Helper()
	if err := a.AddSplits(splits); err != nil {
		t.Fatal(err)
	}
}

func assertState(t *testing.T, a *Allocator, split int64, state SplitState, owner int, ret int) {
	t.Helper()
	info, err := a.State(split)
	if err != nil {
		t.Fatal(err)
	}
	if info.State != state || info.Owner != owner || info.Ret != ret {
		t.Fatalf("state(%d) = %+v, want state %v owner %d ret %d", split, info, state, owner, ret)
	}
}

func mustRequest(t *testing.T, a *Allocator, reader int) RequestResult {
	t.Helper()
	result, err := a.RequestSplit(reader)
	if err != nil {
		t.Fatal(err)
	}
	return result
}
