package coord

import (
	"errors"
	"testing"
	"time"

	"ontology/participant"
	"ontology/vote"
)

func TestQueryZeroValueBeforeDecision(t *testing.T) {
	txn, _ := agreeTxn(t, "a", "b")
	snap := txn.Query()
	if snap.Phase != PhaseVoting {
		t.Fatalf("phase: %v", snap.Phase)
	}
	if snap.Decision != (vote.Decision{}) {
		t.Fatalf("undecided decision must be zero value, got %+v", snap.Decision)
	}
	if len(snap.Members) != 2 {
		t.Fatalf("members: %+v", snap.Members)
	}
	for _, m := range snap.Members {
		if m.State != participant.StatePending {
			t.Fatalf("%s: %v", m.ID, m.State)
		}
	}
}

func TestQueryStableAfterDecision(t *testing.T) {
	txn, _ := agreeTxn(t, "a", "b")
	txn.Drive()
	first := txn.Query()
	second := txn.Query()
	if first.Phase != second.Phase || first.Decision != second.Decision {
		t.Fatalf("unstable query: %+v vs %+v", first, second)
	}
	if len(first.Members) != len(second.Members) {
		t.Fatal("member count changed")
	}
	for i := range first.Members {
		if first.Members[i] != second.Members[i] {
			t.Fatalf("member %d changed", i)
		}
	}
	if first.Decision.Verdict != vote.VerdictCommit {
		t.Fatalf("decision: %+v", first.Decision)
	}
}

func TestReplayAfterCrashIsIdempotent(t *testing.T) {
	txn, members := agreeTxn(t, "a", "b", "c")
	txn.Drive()
	before := txn.Query()
	if err := txn.Replay(); err != nil {
		t.Fatalf("replay: %v", err)
	}
	if err := txn.Replay(); err != nil {
		t.Fatalf("replay 2: %v", err)
	}
	after := txn.Query()
	if before.Phase != after.Phase || before.Decision != after.Decision {
		t.Fatal("replay changed the decision")
	}
	for i := range before.Members {
		if before.Members[i] != after.Members[i] {
			t.Fatalf("replay changed state of %s", after.Members[i].ID)
		}
	}
	for id, p := range members {
		if p.CommitCount() != 1 {
			t.Fatalf("%s: commit count %d after replay, want 1", id, p.CommitCount())
		}
	}
}

func TestReplayBeforeDecisionFails(t *testing.T) {
	txn, _ := agreeTxn(t, "a")
	if err := txn.Replay(); !errors.Is(err, ErrNotDecided) {
		t.Fatalf("want ErrNotDecided, got %v", err)
	}
}

func TestEmptyParticipantSetCommits(t *testing.T) {
	txn := NewTxn(newFakeClock().now, time.Minute)
	d := txn.Drive()
	if d.Verdict != vote.VerdictCommit || d.Reason != vote.ReasonAllAgreed {
		t.Fatalf("empty set decision: %+v", d)
	}
	snap := txn.Query()
	if snap.Phase != PhaseCommit || len(snap.Members) != 0 {
		t.Fatalf("snapshot: %+v", snap)
	}
	if err := txn.Replay(); err != nil {
		t.Fatalf("replay on empty set: %v", err)
	}
}

func TestSendCommitBeforePrepareIsIllegal(t *testing.T) {
	txn, members := agreeTxn(t, "a")
	if err := txn.Send("a", InstrCommit); !errors.Is(err, participant.ErrNotPrepared) {
		t.Fatalf("want ErrNotPrepared, got %v", err)
	}
	if members["a"].State() != participant.StatePending {
		t.Fatalf("state changed: %v", members["a"].State())
	}
}
