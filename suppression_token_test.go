package ontology

import (
	"errors"
	"testing"
)

func TestConfirmationTokenOverwriteAndTTLBoundaries(t *testing.T) {
	manager := newTestManager(t)
	if err := manager.Unsub("a@example.com", 10); err != nil {
		t.Fatal(err)
	}
	first, err := manager.RequestConfirm("a@example.com", 20)
	if err != nil {
		t.Fatal(err)
	}
	second, err := manager.RequestConfirm("a@example.com", 30)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("input=RequestConfirm at=[20,30] output=seq=[%d,%d] basis=new token overwrites old token and seq never reuses", first, second)
	if first != 1 || second != 2 {
		t.Fatalf("seqs = (%d, %d), want (1, 2)", first, second)
	}
	if err := manager.Confirm("a@example.com", first, 40); !errors.Is(err, ErrStaleToken) {
		t.Fatalf("old token error = %v, want %v", err, ErrStaleToken)
	}
	if err := manager.Confirm("a@example.com", second, 79); err != nil {
		t.Fatalf("one time unit before TTL: %v", err)
	}

	if err := manager.Unsub("a@example.com", 81); err != nil {
		t.Fatal(err)
	}
	third, err := manager.RequestConfirm("a@example.com", 90)
	if err != nil {
		t.Fatal(err)
	}
	err = manager.Confirm("a@example.com", third, 140)
	t.Logf("input=Confirm seq=%d issuedAt=90 now=140 TTL=50 output=%v basis=issuedAt+TTL equality is expired", third, err)
	if !errors.Is(err, ErrTokenExpired) {
		t.Fatalf("exact TTL error = %v, want %v", err, ErrTokenExpired)
	}
}

func TestTokenAfterSuppressionStartIsInvalid(t *testing.T) {
	manager := newTestManager(t)
	if err := manager.Unsub("a@example.com", 10); err != nil {
		t.Fatal(err)
	}
	seq, err := manager.RequestConfirm("a@example.com", 10)
	if err != nil {
		t.Fatal(err)
	}
	err = manager.Confirm("a@example.com", seq, 20)
	t.Logf("input=Confirm seq=%d issuedAt=10 since=10 output=%v basis=token must be strictly newer than latest suppression start", seq, err)
	if !errors.Is(err, ErrTokenBeforeSuppression) {
		t.Fatalf("error = %v, want %v", err, ErrTokenBeforeSuppression)
	}
}

func TestComplaintCannotRecover(t *testing.T) {
	manager := newTestManager(t)
	if err := manager.Complaint("a@example.com", 10); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.RequestConfirm("a@example.com", 20); !errors.Is(err, ErrRecoveryForbidden) {
		t.Fatalf("request error = %v, want %v", err, ErrRecoveryForbidden)
	}

	state := manager.addresses["a@example.com"]
	state.hasToken = true
	state.tokenSeq = 99
	state.tokenIssuedAt = 20
	err := manager.Confirm("a@example.com", 99, 30)
	t.Logf("input=Confirm complaint output=%v basis=complaint is checked before recoverable-state checks", err)
	if !errors.Is(err, ErrComplaintActive) {
		t.Fatalf("confirm error = %v, want %v", err, ErrComplaintActive)
	}
	assertSuppressed(t, manager, "a@example.com", 30, true, Complaint)
}

func TestConfirmCheckOrder(t *testing.T) {
	manager := newTestManager(t)

	if err := manager.Confirm("a@example.com", 1, 1); !errors.Is(err, ErrNoConfirmationToken) {
		t.Fatalf("absent token error = %v", err)
	}

	if err := manager.Unsub("a@example.com", 1); err != nil {
		t.Fatal(err)
	}
	seq, err := manager.RequestConfirm("a@example.com", 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Confirm("a@example.com", seq+1, 3); !errors.Is(err, ErrStaleToken) {
		t.Fatalf("stale token error = %v", err)
	}
	if err := manager.Confirm("a@example.com", seq, 52); !errors.Is(err, ErrTokenExpired) {
		t.Fatalf("expired token error = %v", err)
	}

	state := manager.addresses["a@example.com"]
	state.reason = None
	state.hasToken = true
	state.tokenSeq = seq
	state.tokenIssuedAt = 10
	manager.maxNow = 10
	if err := manager.Confirm("a@example.com", seq, 11); !errors.Is(err, ErrNotRecoverable) {
		t.Fatalf("not recoverable error = %v", err)
	}
}

func TestRequestConfirmRejectionsDoNotConsumeTokenNumber(t *testing.T) {
	manager := newTestManager(t)
	if _, err := manager.RequestConfirm("unknown@example.com", 1); !errors.Is(err, ErrNoRecoveryNeeded) {
		t.Fatalf("unknown address request error = %v", err)
	}
	if err := manager.Hard("a@example.com", 2); err != nil {
		t.Fatal(err)
	}
	seq, err := manager.RequestConfirm("a@example.com", 3)
	if err != nil {
		t.Fatal(err)
	}
	if seq != 1 {
		t.Fatalf("seq = %d, want rejected request not to consume global sequence", seq)
	}
}
