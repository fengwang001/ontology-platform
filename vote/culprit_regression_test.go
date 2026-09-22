package vote

import "testing"

// Regression: the culprit must be the FIRST rejecter in registration
// order, not the last. The finalization scan used to walk the
// registration order backwards, so with multiple rejecters the culprit
// was pinned on the last one. This pins the contract: "Culprit names
// the first participant in registration order matching the verdict
// reason".
func TestCulpritIsFirstRejecterInRegistrationOrder(t *testing.T) {
	c := mustCollector(t, "a", "b", "c")
	for _, id := range []string{"a", "b", "c"} {
		ballot := BallotAgree
		if id != "c" {
			ballot = BallotReject
		}
		if err := c.Cast(id, ballot); err != nil {
			t.Fatalf("Cast(%s): %v", id, err)
		}
	}
	d, ok := c.Decision()
	if !ok {
		t.Fatal("expected decision")
	}
	if d.Verdict != VerdictAbort || d.Reason != ReasonRejected {
		t.Fatalf("unexpected decision: %+v", d)
	}
	if d.Culprit != "a" {
		t.Fatalf("culprit = %q, want %q (first rejecter in registration order)", d.Culprit, "a")
	}
}
