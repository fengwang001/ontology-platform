package vote

import (
	"errors"
	"testing"
)

// TestCulpritIsFirstRejecterInRegistrationOrder pins the contract that
// Culprit names the FIRST participant in registration order matching
// the ruling reason. With several rejecters the decision must blame
// the earliest one, not the last.
func TestCulpritIsFirstRejecterInRegistrationOrder(t *testing.T) {
	c := mustCollector(t, "a", "b", "c")
	c.Cast("a", BallotReject)
	c.Cast("b", BallotReject)
	c.Cast("c", BallotAgree)
	d, ok := c.Decision()
	if !ok || d.Verdict != VerdictAbort || d.Reason != ReasonRejected {
		t.Fatalf("unexpected decision: %+v ok=%v", d, ok)
	}
	if d.Culprit != "a" {
		t.Fatalf("culprit must be the first rejecter in registration order, got %q", d.Culprit)
	}
}

// TestTimeoutAfterBallotIsAlreadyAccounted pins the contract that each
// participant is accounted at most once: reporting a timeout for a
// participant that already voted must return ErrAlreadyAccounted,
// leave the recorded state untouched and not consume one of the
// "still missing" slots (which would finalize the decision early).
func TestTimeoutAfterBallotIsAlreadyAccounted(t *testing.T) {
	c := mustCollector(t, "a", "b")
	if err := c.Cast("a", BallotAgree); err != nil {
		t.Fatalf("Cast: %v", err)
	}
	if err := c.Timeout("a"); !errors.Is(err, ErrAlreadyAccounted) {
		t.Fatalf("want ErrAlreadyAccounted, got %v", err)
	}
	if _, ok := c.Decision(); ok {
		t.Fatal("double accounting must not finalize the decision while b is still missing")
	}
	if err := c.Cast("b", BallotAgree); err != nil {
		t.Fatalf("Cast(b): %v", err)
	}
	d, ok := c.Decision()
	if !ok || d.Verdict != VerdictCommit || d.Reason != ReasonAllAgreed {
		t.Fatalf("unexpected decision: %+v ok=%v", d, ok)
	}
}
