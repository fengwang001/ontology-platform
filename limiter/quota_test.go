package limiter

import (
	"errors"
	"testing"
	"time"
)

func TestUpdateQuotaShrinkTruncatesGrowKeeps(t *testing.T) {
	c := &fakeClock{t: time.Unix(1000, 0)}
	l := newLimiter(t, c, 1000, 1)
	mustRegister(t, l, "a", 10, 1)
	if err := l.UpdateQuota("a", mustQuota(t, 4, 1)); err != nil {
		t.Fatalf("UpdateQuota: %v", err)
	}
	tok, _, q, _ := l.Stats("a")
	if tok != 4 {
		t.Fatalf("shrink: balance = %v, want truncated 4", tok)
	}
	if q.Capacity != 4 || q.RatePerSec != 1 {
		t.Fatalf("quota = %+v, want {4 1}", q)
	}
	if err := l.UpdateQuota("a", mustQuota(t, 50, 2)); err != nil {
		t.Fatalf("UpdateQuota: %v", err)
	}
	tok, _, q, _ = l.Stats("a")
	if tok != 4 {
		t.Fatalf("grow: balance = %v, want unchanged 4 (no free refill)", tok)
	}
	if q.Capacity != 50 || q.RatePerSec != 2 {
		t.Fatalf("quota = %+v, want {50 2}", q)
	}
	// The new rate applies from now on.
	c.advance(3 * time.Second) // +2*3 = 6
	tok, _, _, _ = l.Stats("a")
	if tok != 10 {
		t.Fatalf("after refill at new rate: balance = %v, want 10", tok)
	}
}

func TestUpdateQuotaUnknownTenant(t *testing.T) {
	c := &fakeClock{t: time.Unix(1000, 0)}
	l := newLimiter(t, c, 10, 1)
	err := l.UpdateQuota("ghost", mustQuota(t, 5, 1))
	if !errors.Is(err, ErrTenantNotFound) {
		t.Fatalf("err = %v, want ErrTenantNotFound", err)
	}
}

func TestStatsReadOnlyAndStable(t *testing.T) {
	c := &fakeClock{t: time.Unix(1000, 0)}
	l := newLimiter(t, c, 100, 1)
	mustRegister(t, l, "a", 10, 1)
	if err := l.Allow("a", 3); err != nil {
		t.Fatalf("Allow: %v", err)
	}
	t1, g1, q1, ok1 := l.Stats("a")
	t2, g2, q2, ok2 := l.Stats("a")
	if !ok1 || !ok2 {
		t.Fatal("Stats on registered tenant must report ok")
	}
	if t1 != t2 || g1 != g2 || q1 != q2 {
		t.Fatalf("two reads differ: (%v,%v,%v) vs (%v,%v,%v)", t1, g1, q1, t2, g2, q2)
	}
	if t1 != 7 || g1 != 97 {
		t.Fatalf("Stats = %v/%v, want 7/97", t1, g1)
	}
	if _, _, _, ok := l.Stats("ghost"); ok {
		t.Fatal("Stats on unknown tenant must report !ok")
	}
}

func TestUnregisterAndReregister(t *testing.T) {
	c := &fakeClock{t: time.Unix(1000, 0)}
	l := newLimiter(t, c, 100, 0.001)
	mustRegister(t, l, "a", 10, 0.001)
	if err := l.Allow("a", 10); err != nil { // drain tenant, global now 90
		t.Fatalf("Allow: %v", err)
	}
	if err := l.Unregister("a"); err != nil {
		t.Fatalf("Unregister: %v", err)
	}
	if err := l.Unregister("a"); !errors.Is(err, ErrTenantNotFound) {
		t.Fatalf("double unregister: err = %v, want ErrTenantNotFound", err)
	}
	if _, g, _, ok := l.Stats("a"); ok || g != 0 {
		t.Fatalf("unregistered Stats: ok=%v global=%v, want false/0", ok, g)
	}
	mustRegister(t, l, "a", 10, 0.001) // fresh full bucket
	tok, g, _, ok := l.Stats("a")
	if !ok || tok != 10 {
		t.Fatalf("re-registered tenant: balance = %v, want full 10", tok)
	}
	if g != 90 {
		t.Fatalf("global balance = %v, want 90 (unregister must not touch it)", g)
	}
}

func TestRegisterDuplicateRejected(t *testing.T) {
	c := &fakeClock{t: time.Unix(1000, 0)}
	l := newLimiter(t, c, 10, 1)
	mustRegister(t, l, "a", 5, 1)
	if err := l.Register("a", mustQuota(t, 5, 1)); !errors.Is(err, ErrTenantExists) {
		t.Fatalf("err = %v, want ErrTenantExists", err)
	}
}

func TestTenantIsolationAndGlobalExhaustion(t *testing.T) {
	c := &fakeClock{t: time.Unix(1000, 0)}
	l := newLimiter(t, c, 12, 0.001)
	mustRegister(t, l, "a", 10, 0.001)
	mustRegister(t, l, "b", 10, 0.001)
	if err := l.Allow("a", 10); err != nil { // a drained, global 2 left
		t.Fatalf("Allow a: %v", err)
	}
	if err := l.Allow("a", 1); !errors.Is(err, ErrTenantQuota) {
		t.Fatalf("a drained: err = %v, want ErrTenantQuota", err)
	}
	if err := l.Allow("b", 2); err != nil { // b unaffected by a
		t.Fatalf("b must be unaffected by a: %v", err)
	}
	// Global bucket is now empty: every tenant is rejected with ErrGlobalQuota.
	if err := l.Allow("b", 1); !errors.Is(err, ErrGlobalQuota) {
		t.Fatalf("global empty, b: err = %v, want ErrGlobalQuota", err)
	}
	mustRegister(t, l, "c", 10, 0.001)
	if err := l.Allow("c", 1); !errors.Is(err, ErrGlobalQuota) {
		t.Fatalf("global empty, c: err = %v, want ErrGlobalQuota", err)
	}
}
