package limiter

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ontology/policy"
)

func TestSetQuotaShrinkTruncatesGrowKeeps(t *testing.T) {
	c := &clock{t: time.Unix(1000, 0)}
	l := newLimiter(t, c, 1000, 0)
	mustRegister(t, l, "a", 10, 0)
	if err := l.SetQuota("a", policy.Quota{Capacity: 4, RatePerSec: 0}); err != nil {
		t.Fatalf("SetQuota shrink: %v", err)
	}
	s, _ := l.Inspect("a")
	if s.TenantBalance != 4 || s.Quota.Capacity != 4 {
		t.Fatalf("after shrink: balance=%v cap=%v, want 4/4", s.TenantBalance, s.Quota.Capacity)
	}
	if err := l.Allow("a", 3); err != nil {
		t.Fatalf("Allow: %v", err)
	}
	if err := l.SetQuota("a", policy.Quota{Capacity: 50, RatePerSec: 0}); err != nil {
		t.Fatalf("SetQuota grow: %v", err)
	}
	s, _ = l.Inspect("a")
	if s.TenantBalance != 1 { // grown capacity must not top up
		t.Fatalf("after grow: balance=%v, want 1 (no top-up)", s.TenantBalance)
	}
	if err := l.SetQuota("ghost", policy.Quota{Capacity: 1, RatePerSec: 1}); !errors.Is(err, ErrTenantNotFound) {
		t.Fatalf("SetQuota ghost: got %v, want ErrTenantNotFound", err)
	}
}

func TestTenantIsolationAndGlobalExhaustion(t *testing.T) {
	c := &clock{t: time.Unix(1000, 0)}
	l := newLimiter(t, c, 12, 0)
	mustRegister(t, l, "a", 5, 0)
	mustRegister(t, l, "b", 5, 0)
	if err := l.Allow("a", 5); err != nil { // tenant a exhausted
		t.Fatalf("drain a: %v", err)
	}
	if err := l.Allow("a", 1); !errors.Is(err, ErrTenantQuota) {
		t.Fatalf("a again: got %v, want ErrTenantQuota", err)
	}
	if err := l.Allow("b", 5); err != nil { // b unaffected
		t.Fatalf("b should be unaffected: %v", err)
	}
	// Global now holds 2; both tenants are empty. Refill time and
	// watch the global bucket reject everyone.
	if err := l.SetQuota("a", policy.Quota{Capacity: 5, RatePerSec: 10}); err != nil {
		t.Fatalf("SetQuota: %v", err)
	}
	if err := l.SetQuota("b", policy.Quota{Capacity: 5, RatePerSec: 10}); err != nil {
		t.Fatalf("SetQuota: %v", err)
	}
	c.advance(time.Second) // tenants refill to 5, global stays 2
	if err := l.Allow("a", 3); !errors.Is(err, ErrGlobalQuota) {
		t.Fatalf("a: got %v, want ErrGlobalQuota", err)
	}
	if err := l.Allow("b", 3); !errors.Is(err, ErrGlobalQuota) {
		t.Fatalf("b: got %v, want ErrGlobalQuota", err)
	}
}

func TestInspectIsReadOnlyAndStable(t *testing.T) {
	c := &clock{t: time.Unix(1000, 0)}
	l := newLimiter(t, c, 100, 1)
	mustRegister(t, l, "a", 10, 1)
	if err := l.Allow("a", 4); err != nil {
		t.Fatalf("Allow: %v", err)
	}
	s1, ok := l.Inspect("a")
	if !ok {
		t.Fatal("tenant a missing")
	}
	s2, ok := l.Inspect("a")
	if !ok {
		t.Fatal("tenant a missing on second read")
	}
	if s1 != s2 {
		t.Fatalf("two reads differ: %+v vs %+v", s1, s2)
	}
	if s1.TenantBalance != 6 || s1.GlobalBalance != 96 {
		t.Fatalf("snapshot = %+v, want balances 6/96", s1)
	}
	if _, ok := l.Inspect("ghost"); ok {
		t.Fatal("ghost tenant should not exist")
	}
}

func TestUnregisterAndReregister(t *testing.T) {
	c := &clock{t: time.Unix(1000, 0)}
	l := newLimiter(t, c, 100, 0)
	mustRegister(t, l, "a", 10, 0)
	if err := l.Allow("a", 7); err != nil {
		t.Fatalf("Allow: %v", err)
	}
	if err := l.Unregister("a"); err != nil {
		t.Fatalf("Unregister: %v", err)
	}
	if got := l.GlobalBalance(); got != 93 {
		t.Fatalf("global after unregister = %v, want 93 (untouched)", got)
	}
	if err := l.Unregister("a"); !errors.Is(err, ErrTenantNotFound) {
		t.Fatalf("double unregister: got %v, want ErrTenantNotFound", err)
	}
	mustRegister(t, l, "a", 10, 0) // fresh full bucket
	s, ok := l.Inspect("a")
	if !ok || s.TenantBalance != 10 {
		t.Fatalf("re-registered balance = %v, want full 10", s.TenantBalance)
	}
	if err := l.Register("a", policy.Quota{Capacity: 1, RatePerSec: 1}); !errors.Is(err, ErrTenantExists) {
		t.Fatalf("re-register: got %v, want ErrTenantExists", err)
	}
}

func TestConcurrentNoOversell(t *testing.T) {
	c := &clock{t: time.Unix(1000, 0)}
	const tenants = 8
	const capPerTenant = 50
	const globalCap = 200
	l := newLimiter(t, c, globalCap, 0)
	for i := 0; i < tenants; i++ {
		mustRegister(t, l, string(rune('a'+i)), capPerTenant, 0)
	}
	var perTenant [tenants]atomic.Int64
	var global atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < tenants; i++ {
		for j := 0; j < 16; j++ {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				name := string(rune('a' + idx))
				for k := 0; k < 200; k++ {
					if l.Allow(name, 1) == nil {
						perTenant[idx].Add(1)
						global.Add(1)
					}
				}
			}(i)
		}
	}
	wg.Wait()
	for i := 0; i < tenants; i++ {
		if got := perTenant[i].Load(); got > capPerTenant {
			t.Fatalf("tenant %d admitted %d > capacity %d", i, got, capPerTenant)
		}
	}
	if got := global.Load(); got > globalCap {
		t.Fatalf("global admitted %d > capacity %d", got, globalCap)
	}
	if got := global.Load(); got != globalCap {
		t.Fatalf("global admitted %d, want exactly %d (no tokens lost)", got, globalCap)
	}
}
