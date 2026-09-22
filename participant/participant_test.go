package participant

import (
	"errors"
	"testing"

	"ontology/vote"
)

func TestHappyPathPendingToCommitted(t *testing.T) {
	p := New("a")
	if p.State() != Pending {
		t.Fatalf("initial state = %v, want Pending", p.State())
	}
	if err := p.Prepare(); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if err := p.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if p.State() != Committed || p.CommitCount() != 1 {
		t.Fatalf("state=%v count=%d, want Committed/1", p.State(), p.CommitCount())
	}
}

func TestCommitIsIdempotent(t *testing.T) {
	p := New("a")
	_ = p.Prepare()
	_ = p.Commit()
	for i := 0; i < 3; i++ {
		if err := p.Commit(); err != nil {
			t.Fatalf("repeat Commit: %v", err)
		}
	}
	if p.State() != Committed || p.CommitCount() != 1 {
		t.Fatalf("state=%v count=%d, want Committed/1", p.State(), p.CommitCount())
	}
}

func TestAbortIsIdempotent(t *testing.T) {
	p := New("a")
	_ = p.Prepare()
	_ = p.Abort()
	for i := 0; i < 3; i++ {
		if err := p.Abort(); err != nil {
			t.Fatalf("repeat Abort: %v", err)
		}
	}
	if p.State() != Aborted {
		t.Fatalf("state=%v, want Aborted", p.State())
	}
}

func TestIllegalTransitionsAreDistinctAndKeepState(t *testing.T) {
	pending := New("p")
	errBeforePrepare := pending.Commit()

	aborted := New("q")
	_ = aborted.Abort()
	errAfterAbort := aborted.Commit()

	committed := New("r")
	_ = committed.Prepare()
	_ = committed.Commit()
	errAfterCommit := committed.Abort()

	if !errors.Is(errBeforePrepare, ErrCommitBeforePrepare) {
		t.Fatalf("got %v, want ErrCommitBeforePrepare", errBeforePrepare)
	}
	if !errors.Is(errAfterAbort, ErrCommitAfterAbort) {
		t.Fatalf("got %v, want ErrCommitAfterAbort", errAfterAbort)
	}
	if !errors.Is(errAfterCommit, ErrAbortAfterCommit) {
		t.Fatalf("got %v, want ErrAbortAfterCommit", errAfterCommit)
	}
	if errors.Is(errBeforePrepare, errAfterAbort) ||
		errors.Is(errAfterAbort, errAfterCommit) ||
		errors.Is(errBeforePrepare, errAfterCommit) {
		t.Fatal("illegal-transition errors must be mutually distinguishable")
	}
	if pending.State() != Pending || aborted.State() != Aborted ||
		committed.State() != Committed || committed.CommitCount() != 1 {
		t.Fatal("illegal transitions must not change state")
	}
}

func TestApplyDecision(t *testing.T) {
	c := New("c")
	_ = c.Prepare()
	if err := c.Apply(vote.Commit); err != nil || c.State() != Committed {
		t.Fatalf("Apply(Commit): err=%v state=%v", err, c.State())
	}
	a := New("a")
	_ = a.Prepare()
	if err := a.Apply(vote.Abort); err != nil || a.State() != Aborted {
		t.Fatalf("Apply(Abort): err=%v state=%v", err, a.State())
	}
	u := New("u")
	if err := u.Apply(vote.Undecided); !errors.Is(err, ErrApplyUndecided) {
		t.Fatalf("Apply(Undecided): got %v, want ErrApplyUndecided", err)
	}
}

func TestPrepareInFinalStateFails(t *testing.T) {
	p := New("a")
	_ = p.Abort()
	if err := p.Prepare(); !errors.Is(err, ErrPrepareInFinalState) {
		t.Fatalf("got %v, want ErrPrepareInFinalState", err)
	}
}
