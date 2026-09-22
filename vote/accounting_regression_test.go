package vote

import (
	"errors"
	"testing"
)

// Regression: a participant that already cast a ballot must not be
// accounted a second time when a timeout is later reported for it.
// Timeout used to only check the timedOut set, so a vote-then-timeout
// pair was swallowed as a fresh accounting and the "remaining" count
// was decremented twice, letting the decision finalize while someone
// was still unheard. This pins the contract: "each participant is
// accounted at most once; a second accounting returns
// ErrAlreadyAccounted and changes nothing".
func TestTimeoutAfterBallotIsAlreadyAccounted(t *testing.T) {
	c := mustCollector(t, "a", "b")
	if err := c.Cast("a", BallotAgree); err != nil {
		t.Fatalf("Cast(a): %v", err)
	}
	if err := c.Timeout("a"); !errors.Is(err, ErrAlreadyAccounted) {
		t.Fatalf("Timeout(a) = %v, want ErrAlreadyAccounted", err)
	}
	// The duplicate accounting must not have counted: with "b" still
	// unheard, no decision may be finalized yet.
	if d, ok := c.Decision(); ok {
		t.Fatalf("decision finalized early: %+v", d)
	}
	if err := c.Cast("b", BallotAgree); err != nil {
		t.Fatalf("Cast(b): %v", err)
	}
	d, ok := c.Decision()
	if !ok || d.Verdict != VerdictCommit || d.Reason != ReasonAllAgreed {
		t.Fatalf("unexpected decision: %+v ok=%v", d, ok)
	}
}

// Companion check on error priority: once the decision is finalized,
// any further ballot or timeout reports ErrDecided -- even for a
// participant that was already accounted. The fix above must not
// reorder the decided check behind the accounted check.
func TestDecidedTakesPriorityOverAlreadyAccounted(t *testing.T) {
	c := mustCollector(t, "a")
	if err := c.Cast("a", BallotAgree); err != nil {
		t.Fatalf("Cast(a): %v", err)
	}
	if err := c.Timeout("a"); !errors.Is(err, ErrDecided) {
		t.Fatalf("Timeout(a) after decision = %v, want ErrDecided", err)
	}
	if err := c.Cast("a", BallotAgree); !errors.Is(err, ErrDecided) {
		t.Fatalf("Cast(a) after decision = %v, want ErrDecided", err)
	}
}
