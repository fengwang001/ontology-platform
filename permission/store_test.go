package permission

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

func testTime(offset int) time.Time {
	return time.Date(2026, 10, 7, 0, 0, offset, 0, time.UTC)
}

func mustSubmit(t *testing.T, store *Store, change Change) SubmitResult {
	t.Helper()
	result, err := store.Submit(change)
	if err != nil {
		t.Fatalf("Submit(%s): %v", change.ID, err)
	}
	return result
}

func decide(t *testing.T, store *Store, subject, label string, at time.Time) QueryResult {
	t.Helper()
	result, err := store.Decide(Query{Subject: subject, Label: label, At: at})
	if err != nil {
		t.Fatalf("Decide at %s: %v", at, err)
	}
	return result
}

func ruleErrorCode(t *testing.T, err error) ErrorCode {
	t.Helper()
	var ruleErr *RuleError
	if !errors.As(err, &ruleErr) {
		t.Fatalf("expected *RuleError, got %T: %v", err, err)
	}
	return ruleErr.Code
}

func TestSameEffectiveTimeOrder(t *testing.T) {
	store := NewStore()
	label := "read"
	mustSubmit(t, store, Change{
		ID: "a", Subject: "alice", Label: label, Kind: Put, Decision: Allow,
		SubmittedAt: testTime(1), EffectiveAt: testTime(3),
	})
	mustSubmit(t, store, Change{
		ID: "b", Subject: "alice", Label: label, Kind: Put, Decision: Deny,
		SubmittedAt: testTime(2), EffectiveAt: testTime(3),
	})
	mustSubmit(t, store, Change{
		ID: "c", Subject: "alice", Label: label, Kind: Put, Decision: Allow,
		SubmittedAt: testTime(2), EffectiveAt: testTime(3),
	})

	result := decide(t, store, "alice", label, testTime(3))
	if result.Decision != Allow || result.WinningChangeID != "c" {
		t.Fatalf("expected c/allow from submission then ID tie-break, got %s/%s", result.WinningChangeID, result.Decision)
	}
}

func TestHistoricalRepeatableRead(t *testing.T) {
	store := NewStore()
	mustSubmit(t, store, Change{
		ID: "p", Subject: "bob", Label: "read", Kind: Put, Decision: Allow,
		SubmittedAt: testTime(1), EffectiveAt: testTime(3),
	})

	before := decide(t, store, "bob", "read", testTime(2))
	if before.Decision != Deny || before.WinningChangeID != "" {
		t.Fatalf("expected default deny before effective time, got %s/%s", before.WinningChangeID, before.Decision)
	}
	again := decide(t, store, "bob", "read", testTime(2))
	if again.Decision != before.Decision || again.WinningChangeID != before.WinningChangeID {
		t.Fatalf("historical query was not repeatable: %+v != %+v", again, before)
	}

	mustSubmit(t, store, Change{
		ID: "future", Subject: "bob", Label: "read", Kind: Put, Decision: Deny,
		SubmittedAt: testTime(4), EffectiveAt: testTime(8),
	})
	again = decide(t, store, "bob", "read", testTime(2))
	if again.Decision != before.Decision || again.WinningChangeID != before.WinningChangeID {
		t.Fatalf("later submission changed an already materialized historical query: %+v", again)
	}
}

func TestQueuedWithdrawalNeverAffectsDecision(t *testing.T) {
	store := NewStore()
	mustSubmit(t, store, Change{
		ID: "p", Subject: "carol", Label: "read", Kind: Put, Decision: Allow,
		SubmittedAt: testTime(1), EffectiveAt: testTime(10),
	})
	if err := store.Withdraw(WithdrawRequest{ID: "p", WithdrawnAt: testTime(5)}); err != nil {
		t.Fatalf("Withdraw: %v", err)
	}

	for _, offset := range []int{4, 6, 10} {
		result := decide(t, store, "carol", "read", testTime(offset))
		if result.Decision != Deny || result.WinningChangeID != "" || len(result.ExaminedChangeID) != 0 {
			t.Fatalf("withdrawn queued change observed at t=%d: %+v", offset, result)
		}
	}

	err := store.Withdraw(WithdrawRequest{ID: "p", WithdrawnAt: testTime(10)})
	if ruleErrorCode(t, err) != ErrWithdrawAlreadyActive {
		t.Fatalf("expected already active error, got %v", err)
	}
}

func TestRevocationAndCascadeRestore(t *testing.T) {
	store := NewStore()
	changes := []Change{
		{ID: "p", Subject: "dave", Label: "read", Kind: Put, Decision: Allow, SubmittedAt: testTime(0), EffectiveAt: testTime(1)},
		{ID: "r", Subject: "dave", Label: "read", Kind: Revoke, RevokesID: "p", SubmittedAt: testTime(2), EffectiveAt: testTime(3)},
		{ID: "s", Subject: "dave", Label: "read", Kind: Revoke, RevokesID: "r", SubmittedAt: testTime(3), EffectiveAt: testTime(4)},
		{ID: "u", Subject: "dave", Label: "read", Kind: Revoke, RevokesID: "s", SubmittedAt: testTime(4), EffectiveAt: testTime(5)},
	}
	for _, change := range changes {
		mustSubmit(t, store, change)
	}

	cases := []struct {
		at   int
		id   string
		mode Decision
	}{
		{2, "p", Allow},
		{3, "", Deny},
		{4, "p", Allow},
		{5, "", Deny},
	}
	for _, tc := range cases {
		result := decide(t, store, "dave", "read", testTime(tc.at))
		if result.Decision != tc.mode || result.WinningChangeID != tc.id {
			t.Fatalf("at %d expected %s/%s, got %s/%s (examined %v)",
				tc.at, tc.id, tc.mode, result.WinningChangeID, result.Decision, result.ExaminedChangeID)
		}
	}
}

func TestErrorPriorityAndNoSideEffects(t *testing.T) {
	store := NewStore()
	mustSubmit(t, store, Change{
		ID: "known", Subject: "eve", Label: "write", Kind: Put, Decision: Deny,
		SubmittedAt: testTime(2), EffectiveAt: testTime(3),
	})
	before := store.AuditLog()
	_, err := store.Submit(Change{
		ID: "bad", Subject: "eve", Label: "read", Kind: Put, Decision: Allow,
		SubmittedAt: testTime(3), EffectiveAt: testTime(2),
	})
	if ruleErrorCode(t, err) != ErrEffectiveBeforeSubmit {
		t.Fatalf("expected effective-before-submit, got %v", err)
	}
	if len(store.AuditLog()) != len(before)+1 {
		t.Fatal("rejected submission still changed durable queue state")
	}

	_, err = store.Decide(Query{Subject: "eve", Label: "read", At: testTime(0)})
	if ruleErrorCode(t, err) != ErrQueryBeforeHorizon {
		t.Fatalf("expected before-horizon, got %v", err)
	}

	mustSubmit(t, store, Change{
		ID: "old", Subject: "eve", Label: "read", Kind: Put, Decision: Allow,
		SubmittedAt: testTime(2), EffectiveAt: testTime(3),
	})
	decide(t, store, "eve", "read", testTime(4))
	err = store.Withdraw(WithdrawRequest{ID: "old", WithdrawnAt: testTime(4)})
	if ruleErrorCode(t, err) != ErrWithdrawAlreadyActive {
		t.Fatalf("expected active withdrawal error, got %v", err)
	}
}

func TestExaminedProofBoundedByEffectiveSubjectLabelChanges(t *testing.T) {
	store := NewStore()
	mustSubmit(t, store, Change{
		ID: "relevant", Subject: "frank", Label: "read", Kind: Put, Decision: Allow,
		SubmittedAt: testTime(1), EffectiveAt: testTime(2),
	})
	for index := 0; index < 200; index++ {
		mustSubmit(t, store, Change{
			ID: fmt.Sprintf("other-label-%03d", index), Subject: "frank", Label: "write", Kind: Put, Decision: Allow,
			SubmittedAt: testTime(1), EffectiveAt: testTime(2 + index%5),
		})
		mustSubmit(t, store, Change{
			ID: fmt.Sprintf("other-subject-%03d", index), Subject: "grace", Label: "read", Kind: Put, Decision: Allow,
			SubmittedAt: testTime(1), EffectiveAt: testTime(2 + index%5),
		})
	}
	for index := 0; index < 20; index++ {
		mustSubmit(t, store, Change{
			ID: fmt.Sprintf("pending-%02d", index), Subject: "frank", Label: "read", Kind: Put, Decision: Deny,
			SubmittedAt: testTime(3 + index), EffectiveAt: testTime(100 + index),
		})
	}

	result := decide(t, store, "frank", "read", testTime(5))
	if result.Decision != Allow || result.WinningChangeID != "relevant" {
		t.Fatalf("unexpected result: %+v", result)
	}
	if len(result.ExaminedChangeID) != 1 || result.ExaminedChangeID[0] != "relevant" {
		t.Fatalf("observable examination depended on total records: %+v", result.ExaminedChangeID)
	}

	logs := store.AuditLog()
	last := logs[len(logs)-1]
	if last.Call != "Decide" || last.Input == "" || last.Output == "" || last.TemporalBasis == "" {
		t.Fatalf("audit record missing input/output/basis: %+v", last)
	}
}

func TestConcurrentOperationsAreSerializable(t *testing.T) {
	store := NewStore()
	mustSubmit(t, store, Change{
		ID: "base", Subject: "helen", Label: "read", Kind: Put, Decision: Allow,
		SubmittedAt: testTime(1), EffectiveAt: testTime(2),
	})

	var wait sync.WaitGroup
	for index := 0; index < 20; index++ {
		wait.Add(3)
		go func() { defer wait.Done(); _ = store.Withdraw(WithdrawRequest{ID: "base", WithdrawnAt: testTime(3)}) }()
		go func() {
			defer wait.Done()
			_, _ = store.Decide(Query{Subject: "helen", Label: "read", At: testTime(5)})
		}()
		go func() {
			defer wait.Done()
			_, _ = store.Submit(Change{
				ID: "new", Subject: "helen", Label: "read", Kind: Put, Decision: Deny,
				SubmittedAt: testTime(6), EffectiveAt: testTime(7),
			})
		}()
	}
	wait.Wait()

	result := decide(t, store, "helen", "read", testTime(8))
	if result.Decision != Deny || result.WinningChangeID != "new" {
		t.Fatalf("unexpected final state: %+v", result)
	}
}
