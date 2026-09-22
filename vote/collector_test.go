package vote

import (
	"errors"
	"testing"
)

func mustCollector(t *testing.T, ids ...string) *Collector {
	t.Helper()
	c, err := NewCollector(ids)
	if err != nil {
		t.Fatalf("NewCollector: %v", err)
	}
	return c
}

func TestAllAgreeCommits(t *testing.T) {
	c := mustCollector(t, "a", "b", "c")
	for _, id := range []string{"a", "b", "c"} {
		if err := c.Cast(id, BallotAgree); err != nil {
			t.Fatalf("Cast(%s): %v", id, err)
		}
	}
	d, ok := c.Decision()
	if !ok {
		t.Fatal("expected decision")
	}
	if d.Verdict != VerdictCommit || d.Reason != ReasonAllAgreed || d.Culprit != "" {
		t.Fatalf("unexpected decision: %+v", d)
	}
}

func TestOneRejectAborts(t *testing.T) {
	c := mustCollector(t, "a", "b", "c")
	c.Cast("a", BallotAgree)
	c.Cast("b", BallotReject)
	c.Cast("c", BallotAgree)
	d, ok := c.Decision()
	if !ok || d.Verdict != VerdictAbort || d.Reason != ReasonRejected || d.Culprit != "b" {
		t.Fatalf("unexpected decision: %+v ok=%v", d, ok)
	}
}

func TestTimeoutAbortsWithDistinctReason(t *testing.T) {
	c := mustCollector(t, "a", "b")
	c.Cast("a", BallotAgree)
	if err := c.Timeout("b"); err != nil {
		t.Fatalf("Timeout: %v", err)
	}
	d, ok := c.Decision()
	if !ok || d.Verdict != VerdictAbort || d.Reason != ReasonTimeout || d.Culprit != "b" {
		t.Fatalf("unexpected decision: %+v ok=%v", d, ok)
	}
}

func TestRejectWinsOverTimeout(t *testing.T) {
	c := mustCollector(t, "a", "b")
	c.Timeout("a")
	c.Cast("b", BallotReject)
	d, _ := c.Decision()
	if d.Reason != ReasonRejected || d.Culprit != "b" {
		t.Fatalf("unexpected decision: %+v", d)
	}
}

func TestUndecidedIsZeroValue(t *testing.T) {
	c := mustCollector(t, "a", "b")
	c.Cast("a", BallotAgree)
	d, ok := c.Decision()
	if ok {
		t.Fatal("should not be decided")
	}
	if d != (Decision{}) {
		t.Fatalf("undecided decision must be zero value, got %+v", d)
	}
}

func TestEmptySetCommitsImmediately(t *testing.T) {
	c := mustCollector(t)
	d, ok := c.Decision()
	if !ok || d.Verdict != VerdictCommit || d.Reason != ReasonAllAgreed {
		t.Fatalf("unexpected decision: %+v ok=%v", d, ok)
	}
}

func TestDuplicateIDRejected(t *testing.T) {
	if _, err := NewCollector([]string{"a", "a"}); !errors.Is(err, ErrDuplicateParticipant) {
		t.Fatalf("want ErrDuplicateParticipant, got %v", err)
	}
}

func TestUnknownIDRejected(t *testing.T) {
	c := mustCollector(t, "a")
	if err := c.Cast("ghost", BallotAgree); !errors.Is(err, ErrUnknownParticipant) {
		t.Fatalf("want ErrUnknownParticipant, got %v", err)
	}
	if err := c.Timeout("ghost"); !errors.Is(err, ErrUnknownParticipant) {
		t.Fatalf("want ErrUnknownParticipant, got %v", err)
	}
}

func TestLateBallotDoesNotChangeDecision(t *testing.T) {
	c := mustCollector(t, "a", "b")
	c.Cast("a", BallotAgree)
	c.Cast("b", BallotReject)
	before, _ := c.Decision()
	if err := c.Cast("b", BallotAgree); !errors.Is(err, ErrDecided) {
		t.Fatalf("want ErrDecided, got %v", err)
	}
	if err := c.Timeout("a"); !errors.Is(err, ErrDecided) {
		t.Fatalf("want ErrDecided, got %v", err)
	}
	after, _ := c.Decision()
	if after != before {
		t.Fatalf("decision changed: %+v -> %+v", before, after)
	}
}

func TestDuplicateBallotRejected(t *testing.T) {
	c := mustCollector(t, "a", "b")
	c.Cast("a", BallotAgree)
	if err := c.Cast("a", BallotReject); !errors.Is(err, ErrAlreadyAccounted) {
		t.Fatalf("want ErrAlreadyAccounted, got %v", err)
	}
}
