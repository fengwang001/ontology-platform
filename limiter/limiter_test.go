package limiter

import (
	"errors"
	"testing"
	"time"

	"ontology/policy"
)

type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

func mustQuota(t *testing.T, cap, rate float64) policy.Quota {
	t.Helper()
	q, err := policy.Validate(cap, rate)
	if err != nil {
		t.Fatalf("bad test quota: %v", err)
	}
	return q
}

func newLimiter(t *testing.T, c *fakeClock, gCap, gRate float64) *Limiter {
	t.Helper()
	l, err := New(mustQuota(t, gCap, gRate), c.now)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return l
}

func mustRegister(t *testing.T, l *Limiter, id string, cap, rate float64) {
	t.Helper()
	if err := l.Register(id, mustQuota(t, cap, rate)); err != nil {
		t.Fatalf("Register(%s): %v", id, err)
	}
}

func TestBothLevelsCharged(t *testing.T) {
	c := &fakeClock{t: time.Unix(1000, 0)}
	l := newLimiter(t, c, 100, 10)
	mustRegister(t, l, "a", 10, 1)
	if err := l.Allow("a", 4); err != nil {
		t.Fatalf("Allow: %v", err)
	}
	tok, glob, _, ok := l.Stats("a")
	if !ok || tok != 6 || glob != 96 {
		t.Fatalf("tenant=%v global=%v ok=%v, want 6/96/true", tok, glob, ok)
	}
}

func TestGlobalShortageLeavesBothBalancesUntouched(t *testing.T) {
	c := &fakeClock{t: time.Unix(1000, 0)}
	l := newLimiter(t, c, 5, 0.001) // global rate tiny: no refill during test
	mustRegister(t, l, "a", 100, 0.001)
	if err := l.Allow("a", 5); err != nil { // drains global exactly
		t.Fatalf("Allow: %v", err)
	}
	beforeT, beforeG, _, _ := l.Stats("a")
	err := l.Allow("a", 3) // tenant has 95, global has 0
	if !errors.Is(err, ErrGlobalQuota) {
		t.Fatalf("err = %v, want ErrGlobalQuota", err)
	}
	afterT, afterG, _, _ := l.Stats("a")
	if afterT != beforeT || afterG != beforeG {
		t.Fatalf("rejection changed balances: tenant %v->%v global %v->%v",
			beforeT, afterT, beforeG, afterG)
	}
}

func TestTenantShortageReportedWithPriority(t *testing.T) {
	c := &fakeClock{t: time.Unix(1000, 0)}
	l := newLimiter(t, c, 100, 1)
	mustRegister(t, l, "a", 10, 0.001)
	if err := l.Allow("a", 10); err != nil { // drain tenant
		t.Fatalf("Allow: %v", err)
	}
	if err := l.Allow("a", 1); !errors.Is(err, ErrTenantQuota) {
		t.Fatalf("tenant short, global fine: err = %v, want ErrTenantQuota", err)
	}
	// Drain the global bucket too via another tenant, then both are short:
	// the error must still be the tenant one (tenant priority).
	mustRegister(t, l, "b", 100, 0.001)
	if err := l.Allow("b", 90); err != nil {
		t.Fatalf("drain global: %v", err)
	}
	if err := l.Allow("a", 1); !errors.Is(err, ErrTenantQuota) {
		t.Fatalf("both short: err = %v, want ErrTenantQuota", err)
	}
}

func TestUnknownTenantAndBadAmount(t *testing.T) {
	c := &fakeClock{t: time.Unix(1000, 0)}
	l := newLimiter(t, c, 10, 1)
	if err := l.Allow("ghost", 1); !errors.Is(err, ErrTenantNotFound) {
		t.Fatalf("err = %v, want ErrTenantNotFound", err)
	}
	mustRegister(t, l, "a", 10, 1)
	for _, n := range []float64{0, -1} {
		if err := l.Allow("a", n); !errors.Is(err, ErrInvalidAmount) {
			t.Fatalf("n=%v: err = %v, want ErrInvalidAmount", n, err)
		}
	}
	tok, glob, _, _ := l.Stats("a")
	if tok != 10 || glob != 10 {
		t.Fatalf("invalid amounts consumed tokens: tenant=%v global=%v", tok, glob)
	}
}

func TestBurstExceededRejectedImmediately(t *testing.T) {
	c := &fakeClock{t: time.Unix(1000, 0)}
	l := newLimiter(t, c, 10, 1)
	mustRegister(t, l, "a", 10, 1)
	if err := l.Allow("a", 11); !errors.Is(err, ErrBurstExceeded) {
		t.Fatalf("err = %v, want ErrBurstExceeded", err)
	}
	mustRegister(t, l, "b", 100, 1) // tenant cap fine, global cap too small
	if err := l.Allow("b", 11); !errors.Is(err, ErrBurstExceeded) {
		t.Fatalf("err = %v, want ErrBurstExceeded", err)
	}
	tok, glob, _, _ := l.Stats("a")
	if tok != 10 || glob != 10 {
		t.Fatalf("burst rejection consumed tokens: tenant=%v global=%v", tok, glob)
	}
}

func TestInvalidGlobalQuotaRejected(t *testing.T) {
	c := &fakeClock{t: time.Unix(1000, 0)}
	if _, err := New(policy.Quota{Capacity: 0, RatePerSec: 1}, c.now); err == nil {
		t.Fatal("expected error for invalid global quota")
	}
}
