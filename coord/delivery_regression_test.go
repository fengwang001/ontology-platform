package coord

import (
	"testing"
	"time"

	"ontology/participant"
	"ontology/vote"
)

// Regression: the second phase must deliver the decision to EVERY
// registered participant, including those that were never asked to
// prepare because the shared budget was already exhausted. Delivery
// used to skip participants still in StatePending, so on an abort the
// un-asked ones were left pending forever and never released their
// local transaction resources. This pins the contract: "an aborting
// transaction ends with no participant left in the pending state".
func TestAbortReachesParticipantsNeverAsked(t *testing.T) {
	clock := newFakeClock()
	txn := NewTxn(clock.now, time.Minute)
	slow := participant.New("slow", vote.BallotAgree)
	// The first participant burns the whole shared budget, so "late"
	// is never even asked and stays pending through phase one.
	slow.SetPrepareHook(func() { clock.advance(time.Minute) })
	late := participant.New("late", vote.BallotAgree)
	for _, p := range []*participant.Participant{slow, late} {
		if err := txn.Register(p); err != nil {
			t.Fatalf("Register(%s): %v", p.ID(), err)
		}
	}
	d := txn.Drive()
	if d.Verdict != vote.VerdictAbort || d.Reason != vote.ReasonTimeout {
		t.Fatalf("decision: %+v", d)
	}
	if got := late.State(); got != participant.StateAborted {
		t.Fatalf("un-asked participant state = %v, want aborted (abort must reach everyone)", got)
	}
	if got := late.AbortCount(); got != 1 {
		t.Fatalf("un-asked participant aborts = %d, want 1", got)
	}
	snap := txn.Query()
	for _, m := range snap.Members {
		if m.State == participant.StatePending {
			t.Fatalf("%s still pending after abort", m.ID)
		}
	}
}
