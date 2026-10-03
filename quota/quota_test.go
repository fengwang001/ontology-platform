package quota

import "testing"

func TestLazyRefillAndCap(t *testing.T) {
	// rate=1e9 bytes/s, burst=1e9 bytes: refill over 1e12 ms would be
	// 1e21 millibytes, far beyond int64; the bucket must saturate safely.
	b := NewBucket(1_000_000_000, 1_000_000_000)
	if got := b.PeekBalance(1_000_000_000_000); got != 1_000_000_000_000 {
		t.Fatalf("PeekBalance saturate = %d", got)
	}
	if got := b.Tokens(); got != 1_000_000_000_000 {
		t.Fatalf("PeekBalance must not settle: tokens = %d", got)
	}
	b.RefillTo(1_000_000_000_000)
	if b.Tokens() != b.Cap() || b.LastRefill() != 1_000_000_000_000 {
		t.Fatalf("refill state tokens=%d last=%d", b.Tokens(), b.LastRefill())
	}
}

func TestExactRefillArithmetic(t *testing.T) {
	// rate 10 bytes/s -> 10 millibytes/ms. Spend 99000, leaving 1000; a
	// 1-byte debit needs 1900: t=89 yields 1890 (one short), t=90 exactly.
	b := NewBucket(10, 100)
	if !b.Debit(0, 99000) {
		t.Fatal("debit")
	}
	if b.PeekBalance(89) != 1890 {
		t.Fatalf("peek 89 = %d, want 1890", b.PeekBalance(89))
	}
	if b.Debit(89, 1900) {
		t.Fatal("debit must fail one millibyte short without state change")
	}
	if b.LastRefill() != 0 || b.Tokens() != 1000 {
		t.Fatalf("failed debit changed state: tokens=%d last=%d",
			b.Tokens(), b.LastRefill())
	}
	if !b.Debit(90, 1900) {
		t.Fatal("debit must succeed at exact equality")
	}
	if b.Tokens() != 0 {
		t.Fatalf("tokens = %d, want 0", b.Tokens())
	}

	// Refund is capped.
	b.Refund(200, 100000)
	if b.Tokens() != b.Cap() {
		t.Fatalf("refund cap: tokens=%d cap=%d", b.Tokens(), b.Cap())
	}
}

func TestZeroRate(t *testing.T) {
	b := NewBucket(0, 10)
	if !b.Debit(0, 10000) {
		t.Fatal("debit full")
	}
	if b.Debit(1_000_000_000_000, 1) {
		t.Fatal("zero rate must never refill")
	}
}
