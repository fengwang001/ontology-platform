package coord

import (
	"testing"
	"time"

	"ontology/participant"
	"ontology/vote"
)

// TestAbortReachesParticipantsNeverAsked pins the contract that the
// second phase delivers the decision to EVERY registered participant,
// including those still pending because the shared first-phase budget
// was exhausted before they were ever asked. After an abort no
// participant may remain in StatePending.
func TestAbortReachesParticipantsNeverAsked(t *testing.T) {
	clock := newFakeClock()
	txn := NewTxn(clock.now, time.Minute)
	slow := participant.New("slow", vote.BallotAgree)
	// The slow participant burns the whole shared budget, so the
	// later participants are never even asked to prepare.
	slow.SetPrepareHook(func() { clock.advance(2 * time.Minute) })
	never1 := participant.New("never1", vote.BallotAgree)
	never2 := participant.New("never2", vote.BallotAgree)
	for _, p := range []*participant.Participant{slow, never1, never2} {
		if err := txn.Register(p); err != nil {
			t.Fatal(err)
		}
	}
	d := txn.Drive()
	if d.Verdict != vote.VerdictAbort || d.Reason != vote.ReasonTimeout || d.Culprit != "slow" {
		t.Fatalf("decision: %+v", d)
	}
	for id, p := range map[string]*participant.Participant{
		"slow": slow, "never1": never1, "never2": never2,
	} {
		if p.State() != participant.StateAborted {
			t.Fatalf("%s: state=%v, want aborted (abort must reach participants never asked)", id, p.State())
		}
	}
	// Replay after the fix must keep every final state identical and
	// must not repeat local abort actions.
	if err := txn.Replay(); err != nil {
		t.Fatalf("Replay: %v", err)
	}
	for id, p := range map[string]*participant.Participant{
		"slow": slow, "never1": never1, "never2": never2,
	} {
		if p.State() != participant.StateAborted || p.AbortCount() != 1 {
			t.Fatalf("%s after replay: state=%v aborts=%d", id, p.State(), p.AbortCount())
		}
	}
}
