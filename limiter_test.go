package ontology

import (
	"testing"
	"time"
)

func mustLimiter(t *testing.T, capacity, rate int64) *Limiter {
	t.Helper()
	l, err := NewLimiter(capacity, rate)
	if err != nil {
		t.Fatalf("NewLimiter(%d, %d): %v", capacity, rate, err)
	}
	return l
}

func drain(t *testing.T, l *Limiter, tenant string, now time.Time) {
	t.Helper()
	d, err := l.Allow(tenant, l.capacity, now)
	if err != nil || !d.Allowed {
		t.Fatalf("drain: allowed=%v err=%v", d.Allowed, err)
	}
}

// A 100ms step at 7 tokens/s refills exactly 0.7 tokens, and the fraction
// is never truncated: ten steps accumulate to exactly 7 tokens.
func TestFractionalRefillAccumulates(t *testing.T) {
	l := mustLimiter(t, 100, 7)
	base := time.Unix(1_000, 0)
	drain(t, l, "t", base)

	now := base.Add(100 * time.Millisecond)
	if got := l.availableMicro("t", now); got != 700_000 {
		t.Fatalf("after 100ms: got %d micro-tokens, want 700000 (0.7 tokens)", got)
	}
	if got := l.Available("t", now); got != 0 {
		t.Fatalf("after 100ms: got %d whole tokens, want 0", got)
	}
	for i := 1; i < 10; i++ {
		now = now.Add(100 * time.Millisecond)
	}
	if got := l.availableMicro("t", now); got != 7*micro {
		t.Fatalf("after 10x100ms: got %d micro-tokens, want %d", got, 7*micro)
	}
}

// A thousand 1ms advances must equal one 1s advance, exactly.
func TestMicroStepsEqualBigStep(t *testing.T) {
	base := time.Unix(2_000, 0)
	small := mustLimiter(t, 1_000_000, 7)
	big := mustLimiter(t, 1_000_000, 7)
	drain(t, small, "t", base)
	drain(t, big, "t", base)

	now := base
	for i := 0; i < 1000; i++ {
		now = now.Add(time.Millisecond)
		small.availableMicro("t", now)
	}
	big.availableMicro("t", base.Add(time.Second))

	a, b := small.availableMicro("t", now), big.availableMicro("t", base.Add(time.Second))
	if a != b {
		t.Fatalf("1000x1ms gave %d micro-tokens, 1x1s gave %d", a, b)
	}
	if a != 7*micro {
		t.Fatalf("got %d micro-tokens, want exactly %d", a, 7*micro)
	}
}

// Refill past the cap is dropped; the bucket never exceeds capacity.
func TestRefillCapsAtCapacity(t *testing.T) {
	l := mustLimiter(t, 10, 100)
	base := time.Unix(3_000, 0)
	drain(t, l, "t", base)
	if got := l.Available("t", base.Add(time.Hour)); got != 10 {
		t.Fatalf("got %d tokens, want capped 10", got)
	}
	if got := l.availableMicro("t", base.Add(time.Hour)); got != 10*micro {
		t.Fatalf("got %d micro-tokens, want exactly %d", got, 10*micro)
	}
}

// A now earlier than the last call is a zero-length interval: no refill,
// no error, no reset, and the bucket clock does not move backwards.
func TestBackwardTimeIsZeroElapsed(t *testing.T) {
	l := mustLimiter(t, 10, 5)
	base := time.Unix(4_000, 0)
	drain(t, l, "t", base)
	l.availableMicro("t", base.Add(time.Second)) // 5 tokens, last = base+1s

	past := base.Add(500 * time.Millisecond)
	d, err := l.Allow("t", 3, past)
	if err != nil || !d.Allowed {
		t.Fatalf("backward call: allowed=%v err=%v, want allowed", d.Allowed, err)
	}
	if got := l.Available("t", past); got != 2 {
		t.Fatalf("after backward call: got %d tokens, want 2 (no extra refill)", got)
	}
	// The bucket clock stayed at base+1s, so base+2s adds exactly 5 more.
	if got := l.Available("t", base.Add(2*time.Second)); got != 7 {
		t.Fatalf("after real advance: got %d tokens, want 7", got)
	}
}

// Repeating a call at the same instant adds nothing: it is equivalent to
// calling once.
func TestSameInstantIsIdempotent(t *testing.T) {
	l := mustLimiter(t, 10, 5)
	base := time.Unix(5_000, 0)
	drain(t, l, "t", base)
	now := base.Add(time.Second)
	first := l.availableMicro("t", now)
	for i := 0; i < 5; i++ {
		if got := l.availableMicro("t", now); got != first {
			t.Fatalf("call %d at same instant: got %d, want %d", i, got, first)
		}
	}
	d1, _ := l.Allow("t", 0, now)
	d2, _ := l.Allow("t", 0, now)
	if !d1.Allowed || !d2.Allowed || d1 != d2 {
		t.Fatalf("same-instant Allow(0) not idempotent: %v then %v", d1, d2)
	}
}

func TestInvalidConfig(t *testing.T) {
	for _, c := range [][2]int64{{0, 1}, {-1, 1}, {1, -1}, {maxTokens + 1, 1}, {1, maxTokens + 1}} {
		if _, err := NewLimiter(c[0], c[1]); err == nil {
			t.Fatalf("NewLimiter(%d, %d): want error", c[0], c[1])
		}
	}
}
