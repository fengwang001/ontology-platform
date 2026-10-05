package review

import "testing"

func TestLedgerLatestNonCommentWins(t *testing.T) {
	l := NewLedger()
	l.Submit("bob", VerdictApprove)
	l.Submit("bob", VerdictComment)
	if got := l.Entries()["bob"]; got != VerdictApprove {
		t.Fatalf("comment must not override approve, got %v", got)
	}
	l.Submit("bob", VerdictRequestChanges)
	if got := l.Entries()["bob"]; got != VerdictRequestChanges {
		t.Fatalf("latest non-comment verdict must win, got %v", got)
	}
	l.Submit("bob", VerdictComment)
	if got := l.Entries()["bob"]; got != VerdictRequestChanges {
		t.Fatalf("comment must not override request-changes, got %v", got)
	}
}

func TestLedgerCommentAloneStoresNothing(t *testing.T) {
	l := NewLedger()
	l.Submit("bob", VerdictComment)
	if len(l.Entries()) != 0 {
		t.Fatalf("comment alone must store nothing, got %v", l.Entries())
	}
	if l.Dismiss("bob") {
		t.Fatal("dismiss without verdict must report false")
	}
}

func TestLedgerDismissAndClearApprovals(t *testing.T) {
	l := NewLedger()
	l.Submit("a", VerdictApprove)
	l.Submit("b", VerdictRequestChanges)
	l.Submit("c", VerdictApprove)
	l.ClearApprovals()
	entries := l.Entries()
	if len(entries) != 1 || entries["b"] != VerdictRequestChanges {
		t.Fatalf("clear approvals must keep change requests only, got %v", entries)
	}
	if !l.Dismiss("b") {
		t.Fatal("dismiss of recorded verdict must report true")
	}
	if len(l.Entries()) != 0 {
		t.Fatalf("ledger must be empty, got %v", l.Entries())
	}
}

func TestChecksLastReportWinsPerKey(t *testing.T) {
	c := NewChecks()
	c.Report("build", 1, StatusFailure)
	c.Report("build", 1, StatusSuccess)
	c.Report("build", 2, StatusPending)
	if got, _ := c.At("build", 1); got != StatusSuccess {
		t.Fatalf("last report for (build,1) must win, got %v", got)
	}
	if got, _ := c.At("build", 2); got != StatusPending {
		t.Fatalf("(build,2) must be independent, got %v", got)
	}
	if _, ok := c.At("test", 1); ok {
		t.Fatal("unknown key must report not found")
	}
}

func TestStatusPasses(t *testing.T) {
	cases := []struct {
		status Status
		want   bool
	}{
		{StatusPending, false},
		{StatusSuccess, true},
		{StatusFailure, false},
		{StatusNeutral, true},
		{StatusSkipped, true},
	}
	for _, tc := range cases {
		if got := tc.status.Passes(); got != tc.want {
			t.Errorf("Status(%d).Passes() = %v, want %v", tc.status, got, tc.want)
		}
		if !tc.status.Valid() {
			t.Errorf("Status(%d) must be valid", tc.status)
		}
	}
	if Status(-1).Valid() || Status(5).Valid() {
		t.Error("out-of-range statuses must be invalid")
	}
}
