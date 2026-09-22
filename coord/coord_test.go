package coord

import (
	"errors"
	"testing"
	"time"

	"ontology/participant"
	"ontology/vote"
)

func TestUnanimousCommit(t *testing.T) {
	txn, members := agreeTxn(t, "a", "b", "c")
	d := txn.Drive()
	if d.Verdict != vote.VerdictCommit || d.Reason != vote.ReasonAllAgreed {
		t.Fatalf("decision: %+v", d)
	}
	for id, p := range members {
		if p.State() != participant.StateCommitted || p.CommitCount() != 1 {
			t.Fatalf("%s: state=%v commits=%d", id, p.State(), p.CommitCount())
		}
	}
}

func TestOneRejectAbortsEveryone(t *testing.T) {
	txn := NewTxn(newFakeClock().now, time.Minute)
	members := map[string]*participant.Participant{
		"a": participant.New("a", vote.BallotAgree),
		"b": participant.New("b", vote.BallotReject),
		"c": participant.New("c", vote.BallotAgree),
	}
	for _, id := range []string{"a", "b", "c"} {
		if err := txn.Register(members[id]); err != nil {
			t.Fatal(err)
		}
	}
	d := txn.Drive()
	if d.Verdict != vote.VerdictAbort || d.Reason != vote.ReasonRejected || d.Culprit != "b" {
		t.Fatalf("decision: %+v", d)
	}
	for id, p := range members {
		if p.State() != participant.StateAborted {
			t.Fatalf("%s: state=%v, want aborted (agreers must roll back)", id, p.State())
		}
	}
}

func TestTimeoutAbortsAndNamesCulprit(t *testing.T) {
	clock := newFakeClock()
	txn := NewTxn(clock.now, time.Minute)
	fast := participant.New("fast", vote.BallotAgree)
	slow := participant.New("slow", vote.BallotAgree)
	// The slow participant answers exactly at the deadline: too late.
	slow.SetPrepareHook(func() { clock.advance(time.Minute) })
	txn.Register(fast)
	txn.Register(slow)
	d := txn.Drive()
	if d.Verdict != vote.VerdictAbort || d.Reason != vote.ReasonTimeout || d.Culprit != "slow" {
		t.Fatalf("decision: %+v", d)
	}
	if fast.State() != participant.StateAborted || slow.State() != participant.StateAborted {
		t.Fatalf("states: fast=%v slow=%v", fast.State(), slow.State())
	}
}

func TestDeadlineIsLeftClosedRightOpen(t *testing.T) {
	// now == deadline exactly counts as timed out.
	clock := newFakeClock()
	txn := NewTxn(clock.now, time.Minute)
	p := participant.New("a", vote.BallotAgree)
	p.SetPrepareHook(func() { clock.advance(time.Minute) })
	txn.Register(p)
	if d := txn.Drive(); d.Reason != vote.ReasonTimeout {
		t.Fatalf("now==deadline must time out, got %+v", d)
	}

	// One nanosecond before the deadline is still in time.
	clock2 := newFakeClock()
	txn2 := NewTxn(clock2.now, time.Minute)
	q := participant.New("a", vote.BallotAgree)
	q.SetPrepareHook(func() { clock2.advance(time.Minute - time.Nanosecond) })
	txn2.Register(q)
	if d := txn2.Drive(); d.Verdict != vote.VerdictCommit {
		t.Fatalf("now<deadline must commit, got %+v", d)
	}
}

func TestDecisionIsWrittenOnce(t *testing.T) {
	clock := newFakeClock()
	txn := NewTxn(clock.now, time.Minute)
	p := participant.New("a", vote.BallotReject)
	txn.Register(p)
	first := txn.Drive()
	// Late agrees, late rejects and repeated drives must not change it.
	clock.advance(time.Hour)
	second := txn.Drive()
	third := txn.Drive()
	if first != second || first != third {
		t.Fatalf("decision changed: %+v %+v %+v", first, second, third)
	}
	if first.Verdict != vote.VerdictAbort || first.Reason != vote.ReasonRejected {
		t.Fatalf("decision: %+v", first)
	}
}

func TestDuplicateRegisterAndUnknownID(t *testing.T) {
	txn, _ := agreeTxn(t, "a")
	dup := participant.New("a", vote.BallotAgree)
	if err := txn.Register(dup); !errors.Is(err, ErrDuplicateParticipant) {
		t.Fatalf("want ErrDuplicateParticipant, got %v", err)
	}
	if err := txn.Send("ghost", InstrCommit); !errors.Is(err, ErrUnknownParticipant) {
		t.Fatalf("want ErrUnknownParticipant, got %v", err)
	}
	if err := txn.Send("ghost", InstrAbort); !errors.Is(err, ErrUnknownParticipant) {
		t.Fatalf("want ErrUnknownParticipant, got %v", err)
	}
}
