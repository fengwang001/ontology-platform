package limiter

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ontology/policy"
)

func TestTenantIsolation(t *testing.T) {
	l, _ := newLimiter(t, 100, 0, map[string]policy.Quota{
		"a": policy.Must(2, 0),
		"b": policy.Must(2, 0),
	})
	if err := l.Allow("a", 2); err != nil {
		t.Fatal(err)
	}
	if err := l.Allow("a", 1); !errors.Is(err, ErrTenantQuota) {
		t.Fatalf("a should be exhausted, got %v", err)
	}
	if err := l.Allow("b", 2); err != nil {
		t.Fatalf("b must be unaffected by a: %v", err)
	}
}

func TestGlobalExhaustionRejectsEveryone(t *testing.T) {
	l, _ := newLimiter(t, 3, 0, map[string]policy.Quota{
		"a": policy.Must(10, 0),
		"b": policy.Must(10, 0),
	})
	if err := l.Allow("a", 3); err != nil {
		t.Fatal(err)
	}
	if err := l.Allow("b", 1); !errors.Is(err, ErrGlobalQuota) {
		t.Fatalf("global empty: b must be rejected with global cause, got %v", err)
	}
	if err := l.Allow("a", 1); !errors.Is(err, ErrGlobalQuota) {
		t.Fatalf("global empty: a must be rejected with global cause, got %v", err)
	}
}

func TestInspectIsPureAndStable(t *testing.T) {
	l, c := newLimiter(t, 10, 1, map[string]policy.Quota{"a": policy.Must(8, 2)})
	if err := l.Allow("a", 3); err != nil {
		t.Fatal(err)
	}
	c.Advance(time.Second)
	tb1, gb1, q1, ok1 := l.Inspect("a")
	tb2, gb2, q2, ok2 := l.Inspect("a")
	if !ok1 || !ok2 {
		t.Fatal("tenant should exist")
	}
	if tb1 != tb2 || gb1 != gb2 || q1 != q2 {
		t.Fatalf("repeated inspect differs: (%v,%v,%v) vs (%v,%v,%v)", tb1, gb1, q1, tb2, gb2, q2)
	}
	if tb1 != 7 || gb1 != 8 { // 5+2*1 and 7+1*1
		t.Fatalf("inspect values: tenant=%v global=%v, want 7 and 8", tb1, gb1)
	}
	if q1.Capacity != 8 || q1.RatePerSec != 2 {
		t.Fatalf("quota = %+v", q1)
	}
	if tb, gb, _, ok := l.Inspect("ghost"); ok || tb != 0 || gb != 0 {
		t.Fatalf("unknown tenant must return zero values and false")
	}
}

func TestUnregisterAndRecreate(t *testing.T) {
	l, _ := newLimiter(t, 100, 0, map[string]policy.Quota{"a": policy.Must(5, 0)})
	if err := l.Allow("a", 5); err != nil {
		t.Fatal(err)
	}
	if err := l.Unregister("a"); err != nil {
		t.Fatal(err)
	}
	if err := l.Unregister("a"); !errors.Is(err, ErrTenantNotFound) {
		t.Fatalf("double unregister: want ErrTenantNotFound, got %v", err)
	}
	if err := l.Register("a", policy.Must(7, 0)); err != nil {
		t.Fatal(err)
	}
	if _, gb := balances(t, l, "a"); gb != 95 {
		t.Fatalf("unregister must not touch global, got %v", gb)
	}
	if tb, _ := balances(t, l, "a"); tb != 7 {
		t.Fatalf("re-registered tenant must be a fresh full bucket, got %v", tb)
	}
	if err := l.Register("a", policy.Must(1, 0)); !errors.Is(err, ErrTenantExists) {
		t.Fatalf("duplicate register: want ErrTenantExists, got %v", err)
	}
}

func TestConcurrentNoOversell(t *testing.T) {
	const (
		tenantCap = 500
		globalCap = 800
		workers   = 16
		attempts  = 200
	)
	l, _ := newLimiter(t, globalCap, 0, map[string]policy.Quota{
		"a": policy.Must(tenantCap, 0),
		"b": policy.Must(tenantCap, 0),
	})
	var allowedA, allowedB atomic.Int64
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			tenant := "a"
			counter := &allowedA
			if w%2 == 1 {
				tenant = "b"
				counter = &allowedB
			}
			for i := 0; i < attempts; i++ {
				if l.Allow(tenant, 2) == nil {
					counter.Add(2)
				}
			}
		}(w)
	}
	wg.Wait()
	totalA, totalB := allowedA.Load(), allowedB.Load()
	if totalA > tenantCap || totalB > tenantCap {
		t.Fatalf("tenant oversell: a=%d b=%d cap=%d", totalA, totalB, tenantCap)
	}
	if totalA+totalB > globalCap {
		t.Fatalf("global oversell: %d > %d", totalA+totalB, globalCap)
	}
	_, gb, _, _ := l.Inspect("a")
	if float64(totalA+totalB)+gb != globalCap {
		t.Fatalf("token leak: consumed %d + remaining %v != %d", totalA+totalB, gb, globalCap)
	}
}
