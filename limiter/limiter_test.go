package limiter

import (
	"errors"
	"testing"
	"time"

	"ontology/policy"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time          { return c.t }
func (c *clock) advance(d time.Duration) { c.t = c.t.Add(d) }

func newLimiter(t *testing.T, c *clock, globalCap, globalRate float64) *Limiter {
	t.Helper()
	l, err := New(policy.Quota{Capacity: globalCap, RatePerSec: globalRate}, c.now)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return l
}

func mustRegister(t *testing.T, l *Limiter, tenant string, cap, rate float64) {
	t.Helper()
	if err := l.Register(tenant, policy.Quota{Capacity: cap, RatePerSec: rate}); err != nil {
		t.Fatalf("Register(%s): %v", tenant, err)
	}
}

func TestAllowDeductsBothLevels(t *testing.T) {
	c := &clock{t: time.Unix(1000, 0)}
	l := newLimiter(t, c, 100, 0)
	mustRegister(t, l, "a", 10, 0)
	if err := l.Allow("a", 4); err != nil {
		t.Fatalf("Allow: %v", err)
	}
	s, ok := l.Inspect("a")
	if !ok {
		t.Fatal("tenant a missing")
	}
	if s.TenantBalance != 6 || s.GlobalBalance != 96 {
		t.Fatalf("balances = (%v, %v), want (6, 96)", s.TenantBalance, s.GlobalBalance)
	}
}

func TestGlobalShortageRollsBackTenant(t *testing.T) {
	c := &clock{t: time.Unix(1000, 0)}
	l := newLimiter(t, c, 10, 0)
	mustRegister(t, l, "a", 10, 0)
	mustRegister(t, l, "b", 10, 0)
	if err := l.Allow("b", 10); err != nil { // drain the global bucket
		t.Fatalf("drain global: %v", err)
	}
	err := l.Allow("a", 5) // tenant has 10, global has 0
	if !errors.Is(err, ErrGlobalQuota) {
		t.Fatalf("got %v, want ErrGlobalQuota", err)
	}
	s, _ := l.Inspect("a")
	if s.TenantBalance != 10 || s.GlobalBalance != 0 {
		t.Fatalf("balances = (%v, %v), want (10, 0): rollback failed",
			s.TenantBalance, s.GlobalBalance)
	}
}

func TestTenantShortageReportedFirst(t *testing.T) {
	c := &clock{t: time.Unix(1000, 0)}
	l := newLimiter(t, c, 10, 0)
	mustRegister(t, l, "a", 5, 0)
	mustRegister(t, l, "b", 10, 0)
	if err := l.Allow("a", 5); err != nil { // drain tenant a
		t.Fatalf("drain a: %v", err)
	}
	if err := l.Allow("b", 5); err != nil { // drain global
		t.Fatalf("drain global: %v", err)
	}
	// Both buckets are now empty; tenant must be reported first.
	if err := l.Allow("a", 1); !errors.Is(err, ErrTenantQuota) {
		t.Fatalf("got %v, want ErrTenantQuota", err)
	}
	s, _ := l.Inspect("a")
	if s.TenantBalance != 0 || s.GlobalBalance != 0 {
		t.Fatalf("balances = (%v, %v), want (0, 0)", s.TenantBalance, s.GlobalBalance)
	}
}

func TestUnknownTenantAndBadAmount(t *testing.T) {
	c := &clock{t: time.Unix(1000, 0)}
	l := newLimiter(t, c, 10, 0)
	mustRegister(t, l, "a", 10, 0)
	if err := l.Allow("ghost", 1); !errors.Is(err, ErrTenantNotFound) {
		t.Fatalf("got %v, want ErrTenantNotFound", err)
	}
	for _, n := range []float64{0, -1} {
		if err := l.Allow("a", n); !errors.Is(err, ErrInvalidAmount) {
			t.Fatalf("n=%v: got %v, want ErrInvalidAmount", n, err)
		}
	}
	if err := l.Allow("a", 11); !errors.Is(err, ErrExceedsBurst) {
		t.Fatalf("n=11: got %v, want ErrExceedsBurst", err)
	}
	s, _ := l.Inspect("a")
	if s.TenantBalance != 10 || s.GlobalBalance != 10 {
		t.Fatalf("balances = (%v, %v), want (10, 10): invalid calls consumed tokens",
			s.TenantBalance, s.GlobalBalance)
	}
}

func TestRefillCapAndClockRewind(t *testing.T) {
	c := &clock{t: time.Unix(1000, 0)}
	l := newLimiter(t, c, 1000, 0)
	mustRegister(t, l, "a", 10, 4) // 4 tokens/sec
	if err := l.Allow("a", 10); err != nil {
		t.Fatalf("drain: %v", err)
	}
	c.advance(1500 * time.Millisecond) // +6
	s, _ := l.Inspect("a")
	if s.TenantBalance != 6 {
		t.Fatalf("balance = %v, want 6", s.TenantBalance)
	}
	c.advance(10 * time.Second) // far past capacity
	s, _ = l.Inspect("a")
	if s.TenantBalance != 10 {
		t.Fatalf("balance = %v, want capped 10", s.TenantBalance)
	}
	if err := l.Allow("a", 10); err != nil {
		t.Fatalf("drain again: %v", err)
	}
	c.t = c.t.Add(-time.Hour) // rewind: must not go negative
	s, _ = l.Inspect("a")
	if s.TenantBalance != 0 {
		t.Fatalf("balance after rewind = %v, want 0", s.TenantBalance)
	}
}
