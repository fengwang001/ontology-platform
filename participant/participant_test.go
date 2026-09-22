package participant

import (
	"errors"
	"testing"

	"ontology/vote"
)

func TestHappyPath(t *testing.T) {
	p := New("a", vote.BallotAgree)
	if p.State() != StatePending {
		t.Fatalf("initial state: %v", p.State())
	}
	if b := p.Prepare(); b != vote.BallotAgree {
		t.Fatalf("vote: %v", b)
	}
	if p.State() != StatePrepared {
		t.Fatalf("after prepare: %v", p.State())
	}
	if err := p.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if p.State() != StateCommitted || p.CommitCount() != 1 {
		t.Fatalf("state=%v commits=%d", p.State(), p.CommitCount())
	}
}

func TestRejectVoteAbortsLocally(t *testing.T) {
	p := New("a", vote.BallotReject)
	if b := p.Prepare(); b != vote.BallotReject {
		t.Fatalf("vote: %v", b)
	}
	if p.State() != StateAborted {
		t.Fatalf("after reject: %v", p.State())
	}
}

func TestCommitIdempotent(t *testing.T) {
	p := New("a", vote.BallotAgree)
	p.Prepare()
	for i := 0; i < 3; i++ {
		if err := p.Commit(); err != nil {
			t.Fatalf("commit %d: %v", i, err)
		}
	}
	if p.State() != StateCommitted || p.CommitCount() != 1 {
		t.Fatalf("state=%v commits=%d, want committed/1", p.State(), p.CommitCount())
	}
}

func TestAbortIdempotent(t *testing.T) {
	p := New("a", vote.BallotAgree)
	p.Prepare()
	for i := 0; i < 3; i++ {
		if err := p.Abort(); err != nil {
			t.Fatalf("abort %d: %v", i, err)
		}
	}
	if p.State() != StateAborted || p.AbortCount() != 1 {
		t.Fatalf("state=%v aborts=%d, want aborted/1", p.State(), p.AbortCount())
	}
}

func TestIllegalTransitionsAreDistinctAndKeepState(t *testing.T) {
	pending := New("p", vote.BallotAgree)
	if err := pending.Commit(); !errors.Is(err, ErrNotPrepared) {
		t.Fatalf("want ErrNotPrepared, got %v", err)
	}
	if pending.State() != StatePending {
		t.Fatalf("state changed: %v", pending.State())
	}

	aborted := New("q", vote.BallotReject)
	aborted.Prepare()
	if err := aborted.Commit(); !errors.Is(err, ErrCommitAfterAbort) {
		t.Fatalf("want ErrCommitAfterAbort, got %v", err)
	}
	if aborted.State() != StateAborted {
		t.Fatalf("state changed: %v", aborted.State())
	}

	committed := New("r", vote.BallotAgree)
	committed.Prepare()
	committed.Commit()
	if err := committed.Abort(); !errors.Is(err, ErrAbortAfterCommit) {
		t.Fatalf("want ErrAbortAfterCommit, got %v", err)
	}
	if committed.State() != StateCommitted {
		t.Fatalf("state changed: %v", committed.State())
	}

	// All three errors must be mutually distinguishable.
	if errors.Is(ErrNotPrepared, ErrCommitAfterAbort) ||
		errors.Is(ErrCommitAfterAbort, ErrAbortAfterCommit) ||
		errors.Is(ErrNotPrepared, ErrAbortAfterCommit) {
		t.Fatal("illegal-transition errors are not distinct")
	}
}

func TestPrepareHookRuns(t *testing.T) {
	p := New("a", vote.BallotAgree)
	ran := false
	p.SetPrepareHook(func() { ran = true })
	p.Prepare()
	if !ran {
		t.Fatal("hook did not run")
	}
}
