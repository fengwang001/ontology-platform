package participant

import (
	"errors"
	"testing"

	"ontology/vote"
)

func TestHappyPathPendingToCommitted(t *testing.T) {
	p := New("p1")
	if p.State() != Pending {
		t.Fatalf("new participant must be Pending, got %v", p.State())
	}
	if err := p.Prepare(); err != nil {
		t.Fatal(err)
	}
	if p.State() != Prepared {
		t.Fatalf("want Prepared, got %v", p.State())
	}
	if err := p.Commit(); err != nil {
		t.Fatal(err)
	}
	if p.State() != Committed || p.CommitCount() != 1 {
		t.Fatalf("want Committed x1, got %v x%d", p.State(), p.CommitCount())
	}
}

func TestAbortFromPendingAndPrepared(t *testing.T) {
	p1 := New("a")
	if err := p1.Abort(); err != nil || p1.State() != Aborted {
		t.Fatalf("abort from Pending: err=%v state=%v", err, p1.State())
	}
	p2 := New("b")
	_ = p2.Prepare()
	if err := p2.Abort(); err != nil || p2.State() != Aborted {
		t.Fatalf("abort from Prepared: err=%v state=%v", err, p2.State())
	}
}

func TestCommitIdempotentAndCountStaysOne(t *testing.T) {
	p := New("p1")
	_ = p.Prepare()
	for i := 0; i < 3; i++ {
		if err := p.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	if p.State() != Committed || p.CommitCount() != 1 {
		t.Fatalf("repeated commit must be idempotent, got %v x%d", p.State(), p.CommitCount())
	}
}

func TestAbortIdempotent(t *testing.T) {
	p := New("p1")
	_ = p.Prepare()
	for i := 0; i < 3; i++ {
		if err := p.Abort(); err != nil {
			t.Fatal(err)
		}
	}
	if p.State() != Aborted {
		t.Fatalf("repeated abort must be idempotent, got %v", p.State())
	}
}

func TestIllegalTransitionsAreDistinctAndKeepState(t *testing.T) {
	pending := New("a")
	if err := pending.Commit(); !errors.Is(err, ErrNotPrepared) {
		t.Fatalf("commit before prepare: %v", err)
	}
	if pending.State() != Pending {
		t.Fatal("state must stay Pending")
	}

	aborted := New("b")
	_ = aborted.Abort()
	if err := aborted.Commit(); !errors.Is(err, ErrCommitAfterAbort) {
		t.Fatalf("commit after abort: %v", err)
	}
	if aborted.State() != Aborted {
		t.Fatal("state must stay Aborted")
	}

	committed := New("c")
	_ = committed.Prepare()
	_ = committed.Commit()
	if err := committed.Abort(); !errors.Is(err, ErrAbortAfterCommit) {
		t.Fatalf("abort after commit: %v", err)
	}
	if committed.State() != Committed || committed.CommitCount() != 1 {
		t.Fatal("state and count must stay unchanged")
	}

	if errors.Is(ErrNotPrepared, ErrCommitAfterAbort) ||
		errors.Is(ErrCommitAfterAbort, ErrAbortAfterCommit) ||
		errors.Is(ErrNotPrepared, ErrAbortAfterCommit) {
		t.Fatal("the three illegal-transition errors must be distinguishable")
	}
}

func TestPrepareIdempotentAndFinalStateGuard(t *testing.T) {
	p := New("p1")
	_ = p.Prepare()
	if err := p.Prepare(); err != nil {
		t.Fatal("repeated prepare must be idempotent")
	}
	_ = p.Commit()
	if err := p.Prepare(); !errors.Is(err, ErrPrepareAfterFinal) {
		t.Fatalf("prepare after commit: %v", err)
	}
}

func TestDeliverDispatchesByDecision(t *testing.T) {
	p := New("p1")
	if err := p.Deliver(vote.None); err == nil {
		t.Fatal("delivering None must fail")
	}
	_ = p.Prepare()
	if err := p.Deliver(vote.Commit); err != nil || p.State() != Committed {
		t.Fatalf("deliver commit: err=%v state=%v", err, p.State())
	}
	q := New("p2")
	if err := q.Deliver(vote.Abort); err != nil || q.State() != Aborted {
		t.Fatalf("deliver abort: err=%v state=%v", err, q.State())
	}
}
