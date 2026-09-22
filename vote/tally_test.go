package vote

import (
	"errors"
	"testing"
	"time"
)

var base = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func mustTally(t *testing.T, ids ...string) *Tally {
	t.Helper()
	tally, err := NewTally(ids, base.Add(time.Minute))
	if err != nil {
		t.Fatalf("NewTally: %v", err)
	}
	return tally
}

func TestAllYesCommits(t *testing.T) {
	tally := mustTally(t, "a", "b", "c")
	for _, id := range []string{"a", "b", "c"} {
		if err := tally.Cast(id, true); err != nil {
			t.Fatalf("Cast(%s): %v", id, err)
		}
	}
	o := tally.Decide(base)
	if o.Decision != Commit || o.Reason != ReasonNone || o.Culprit != "" {
		t.Fatalf("got %+v, want Commit/None/empty", o)
	}
}

func TestOneNoAbortsAndNamesCulprit(t *testing.T) {
	tally := mustTally(t, "a", "b", "c")
	_ = tally.Cast("a", true)
	_ = tally.Cast("b", false)
	o := tally.Decide(base)
	if o.Decision != Abort || o.Reason != ReasonRejected || o.Culprit != "b" {
		t.Fatalf("got %+v, want Abort/Rejected/b", o)
	}
}

func TestTimeoutAbortsAndNamesMissingVoter(t *testing.T) {
	tally := mustTally(t, "a", "b", "c")
	_ = tally.Cast("a", true)
	_ = tally.Cast("c", true)
	o := tally.Decide(base.Add(2 * time.Minute))
	if o.Decision != Abort || o.Reason != ReasonTimeout || o.Culprit != "b" {
		t.Fatalf("got %+v, want Abort/Timeout/b", o)
	}
}

func TestDeadlineIsLeftClosedRightOpen(t *testing.T) {
	deadline := base.Add(time.Minute)
	tally, err := NewTally([]string{"a", "b"}, deadline)
	if err != nil {
		t.Fatal(err)
	}
	_ = tally.Cast("a", true)
	// 截止时刻前一纳秒：未超时，未裁决。
	if o := tally.Decide(deadline.Add(-time.Nanosecond)); o.Decision != Undecided {
		t.Fatalf("before deadline: got %+v, want Undecided", o)
	}
	// now 恰好等于截止时刻：即算超时。
	o := tally.Decide(deadline)
	if o.Decision != Abort || o.Reason != ReasonTimeout || o.Culprit != "b" {
		t.Fatalf("at deadline: got %+v, want Abort/Timeout/b", o)
	}
}

func TestLateVotesDoNotRewriteDecision(t *testing.T) {
	tally := mustTally(t, "a", "b")
	_ = tally.Cast("a", false)
	first := tally.Decide(base)
	// 迟到的同意票与重复裁决都不改写决议。
	if err := tally.Cast("b", true); err != nil {
		t.Fatalf("late Cast: %v", err)
	}
	if got := tally.Decide(base); got != first {
		t.Fatalf("decision rewritten: first %+v, then %+v", first, got)
	}
}

func TestUnknownVoterRejected(t *testing.T) {
	tally := mustTally(t, "a")
	if err := tally.Cast("ghost", true); !errors.Is(err, ErrUnknownVoter) {
		t.Fatalf("got %v, want ErrUnknownVoter", err)
	}
}

func TestDuplicateVoterRejected(t *testing.T) {
	if _, err := NewTally([]string{"a", "a"}, base); err == nil {
		t.Fatal("want error for duplicate voter id")
	}
}

func TestEmptyVoterSetCommits(t *testing.T) {
	tally := mustTally(t)
	o := tally.Decide(base)
	if o.Decision != Commit || o.Reason != ReasonNone {
		t.Fatalf("got %+v, want Commit/None (vacuous truth)", o)
	}
}

func TestUndecidedBeforeAllVotesAndDeadline(t *testing.T) {
	tally := mustTally(t, "a", "b")
	_ = tally.Cast("a", true)
	if o := tally.Decide(base); o != (Outcome{}) {
		t.Fatalf("got %+v, want zero Outcome", o)
	}
}
