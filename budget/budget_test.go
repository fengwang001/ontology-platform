package budget

import (
	"errors"
	"testing"
)

func TestAcquireRelease(t *testing.T) {
	b := New(10)
	if err := b.TryAcquire(4); err != nil {
		t.Fatalf("acquire 4: %v", err)
	}
	if err := b.TryAcquire(6); err != nil {
		t.Fatalf("acquire 6: %v", err)
	}
	if got := b.Used(); got != 10 {
		t.Fatalf("used = %d, want 10", got)
	}
	b.Release(3)
	if got := b.Used(); got != 7 {
		t.Fatalf("used after release = %d, want 7", got)
	}
}

func TestExhaustedLeavesLedgerUnchanged(t *testing.T) {
	b := New(10)
	if err := b.TryAcquire(8); err != nil {
		t.Fatalf("acquire 8: %v", err)
	}
	err := b.TryAcquire(3)
	if !errors.Is(err, ErrExhausted) {
		t.Fatalf("err = %v, want ErrExhausted", err)
	}
	if got := b.Used(); got != 8 {
		t.Fatalf("used after rejection = %d, want 8", got)
	}
	// Exactly at the limit is still allowed.
	if err := b.TryAcquire(2); err != nil {
		t.Fatalf("acquire to exact limit: %v", err)
	}
	if got := b.Used(); got != 10 {
		t.Fatalf("used = %d, want 10", got)
	}
}

func TestReleaseNeverNegative(t *testing.T) {
	b := New(10)
	b.Release(5)
	if got := b.Used(); got != 0 {
		t.Fatalf("used = %d, want 0", got)
	}
}
