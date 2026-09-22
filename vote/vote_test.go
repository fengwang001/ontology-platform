package vote

import "testing"

func TestDecideAllAgreeCommits(t *testing.T) {
	order := []string{"a", "b", "c"}
	votes := map[string]Outcome{"a": Agree, "b": Agree, "c": Agree}
	v := Decide(order, votes, false)
	if v.Decision != Commit || v.Cause != NoCause || v.Culprit != "" {
		t.Fatalf("want clean COMMIT, got %v", v)
	}
}

func TestDecideOneRejectAborts(t *testing.T) {
	order := []string{"a", "b", "c"}
	votes := map[string]Outcome{"a": Agree, "b": Reject}
	v := Decide(order, votes, false)
	if v.Decision != Abort || v.Cause != Rejected || v.Culprit != "b" {
		t.Fatalf("want ABORT/rejected/b, got %v", v)
	}
}

func TestDecideMissingVoteNotExpiredStaysPending(t *testing.T) {
	order := []string{"a", "b"}
	votes := map[string]Outcome{"a": Agree}
	v := Decide(order, votes, false)
	if v != (Verdict{}) {
		t.Fatalf("want zero verdict while waiting, got %v", v)
	}
}

func TestDecideMissingVoteExpiredTimesOut(t *testing.T) {
	order := []string{"a", "b"}
	votes := map[string]Outcome{"a": Agree}
	v := Decide(order, votes, true)
	if v.Decision != Abort || v.Cause != TimedOut || v.Culprit != "b" {
		t.Fatalf("want ABORT/timeout/b, got %v", v)
	}
}

func TestDecideRejectBeatsTimeoutInRegistrationOrder(t *testing.T) {
	order := []string{"a", "b"}
	votes := map[string]Outcome{"a": Reject}
	v := Decide(order, votes, true)
	if v.Cause != Rejected || v.Culprit != "a" {
		t.Fatalf("want reject reported first, got %v", v)
	}
}

func TestDecideEmptySetCommits(t *testing.T) {
	v := Decide(nil, nil, false)
	if v.Decision != Commit {
		t.Fatalf("empty participant set must commit, got %v", v)
	}
}

func TestZeroVerdictIsPending(t *testing.T) {
	var v Verdict
	if v.Decision != None || v.Cause != NoCause || v.Culprit != "" {
		t.Fatalf("zero verdict must be fully zero, got %v", v)
	}
	if v.String() != "PENDING" {
		t.Fatalf("zero verdict string = %q", v.String())
	}
}
