package limiter

import (
	"errors"
	"sync"
	"testing"
	"time"

	"ontology/policy"
)

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Unix(1_700_000_000, 0)}
}

func (c *fakeClock) Time() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func newLimiter(t *testing.T, globalCap, globalRate float64, tenants map[string]policy.Quota) (*Limiter, *fakeClock) {
	t.Helper()
	c := newFakeClock()
	l := New(policy.Must(globalCap, globalRate), c.Time)
	for name, q := range tenants {
		if err := l.Register(name, q); err != nil {
			t.Fatalf("register %s: %v", name, err)
		}
	}
	return l, c
}

func balances(t *testing.T, l *Limiter, tenant string) (float64, float64) {
	t.Helper()
	tb, gb, _, ok := l.Inspect(tenant)
	if !ok {
		t.Fatalf("tenant %s should exist", tenant)
	}
	return tb, gb
}

func TestBothLevelsMustPass(t *testing.T) {
	l, _ := newLimiter(t, 10, 0, map[string]policy.Quota{"a": policy.Must(5, 0)})
	if err := l.Allow("a", 3); err != nil {
		t.Fatalf("allow: %v", err)
	}
	tb, gb := balances(t, l, "a")
	if tb != 2 || gb != 7 {
		t.Fatalf("tenant=%v global=%v, want 2 and 7", tb, gb)
	}
}

func TestGlobalShortfallLeavesBothBalancesUntouched(t *testing.T) {
	// Tenant has plenty, global has 2; asking 5 must fail on global and
	// leave both balances exactly as before (tenant deduction rolled back).
	l, _ := newLimiter(t, 10, 0, map[string]policy.Quota{"a": policy.Must(8, 0)})
	if err := l.Allow("a", 8); err != nil { // drain tenant to 0? no: leaves global 2
		t.Fatalf("setup allow: %v", err)
	}
	if err := l.Register("b", policy.Must(10, 0)); err != nil {
		t.Fatal(err)
	}
	tb0, gb0 := balances(t, l, "b") // 10, 2
	err := l.Allow("b", 5)
	if !errors.Is(err, ErrGlobalQuota) {
		t.Fatalf("want ErrGlobalQuota, got %v", err)
	}
	tb1, gb1 := balances(t, l, "b")
	if tb1 != tb0 || gb1 != gb0 {
		t.Fatalf("balances changed on rejection: (%v,%v) -> (%v,%v)", tb0, gb0, tb1, gb1)
	}
}

func TestRejectionCausesDistinguishable(t *testing.T) {
	// Tenant "small" cap 2, global cap 10: tenant fails first.
	l, _ := newLimiter(t, 10, 0, map[string]policy.Quota{
		"small": policy.Must(2, 0),
		"big":   policy.Must(10, 0),
	})
	if err := l.Allow("small", 2); err != nil {
		t.Fatal(err)
	}
	err := l.Allow("small", 1)
	if !errors.Is(err, ErrTenantQuota) || errors.Is(err, ErrGlobalQuota) {
		t.Fatalf("want tenant-cause only, got %v", err)
	}
	// Drain global via "big", then both are short for "big": tenant-first.
	if err := l.Allow("big", 8); err != nil { // global 0, tenant 2
		t.Fatal(err)
	}
	err = l.Allow("big", 5) // tenant short (2<5) AND global short (0<5)
	if !errors.Is(err, ErrTenantQuota) {
		t.Fatalf("both short: tenant cause must win, got %v", err)
	}
	// Unregistered tenant is a third, distinguishable error.
	if err := l.Allow("ghost", 1); !errors.Is(err, ErrTenantNotFound) {
		t.Fatalf("want ErrTenantNotFound, got %v", err)
	}
}

func TestInvalidAmountAndBurst(t *testing.T) {
	l, _ := newLimiter(t, 10, 0, map[string]policy.Quota{"a": policy.Must(4, 0)})
	for _, n := range []int{0, -3} {
		if err := l.Allow("a", n); !errors.Is(err, ErrInvalidAmount) {
			t.Fatalf("n=%d: want ErrInvalidAmount, got %v", n, err)
		}
	}
	if err := l.Allow("a", 5); !errors.Is(err, ErrExceedsBurst) {
		t.Fatalf("n>capacity: want ErrExceedsBurst, got %v", err)
	}
	if err := l.Allow("a", 11); !errors.Is(err, ErrExceedsBurst) {
		t.Fatalf("n>global capacity: want ErrExceedsBurst, got %v", err)
	}
	tb, gb := balances(t, l, "a")
	if tb != 4 || gb != 10 {
		t.Fatalf("invalid requests must not consume: tenant=%v global=%v", tb, gb)
	}
}

func TestHotUpdateQuota(t *testing.T) {
	l, c := newLimiter(t, 100, 0, map[string]policy.Quota{"a": policy.Must(10, 1)})
	if err := l.Allow("a", 10); err != nil {
		t.Fatal(err)
	}
	c.Advance(6 * time.Second) // balance 6
	if err := l.SetQuota("a", policy.Must(4, 2)); err != nil {
		t.Fatal(err)
	}
	tb, _ := balances(t, l, "a")
	if tb != 4 {
		t.Fatalf("shrink must truncate balance to 4, got %v", tb)
	}
	if err := l.SetQuota("a", policy.Must(50, 2)); err != nil {
		t.Fatal(err)
	}
	tb, _ = balances(t, l, "a")
	if tb != 4 {
		t.Fatalf("grow must not top up, got %v", tb)
	}
	if err := l.SetQuota("ghost", policy.Must(1, 1)); !errors.Is(err, ErrTenantNotFound) {
		t.Fatalf("want ErrTenantNotFound, got %v", err)
	}
}
