package ontology

import (
	"errors"
	"testing"
	"time"
)

func TestTenantsAreIsolated(t *testing.T) {
	l := newTestLimiter(t, 5, 1)
	now := time.Unix(0, 0)

	// Exhaust tenant "a" completely.
	if ok, _ := l.Allow("a", 5, now); !ok {
		t.Fatal("drain a")
	}
	// Tenant "b" must be completely unaffected: still full.
	for i := 0; i < 5; i++ {
		if ok, err := l.Allow("b", 1, now); !ok || err != nil {
			t.Fatalf("b request %d: ok=%v err=%v", i, ok, err)
		}
	}
	gotA, _ := l.Available("a", now)
	gotB, _ := l.Available("b", now)
	if gotA != 0 || gotB != 0 {
		t.Fatalf("after drains: a=%d b=%d, want 0/0", gotA, gotB)
	}

	if l.ActiveTenants() != 2 {
		t.Fatalf("active=%d, want 2", l.ActiveTenants())
	}

	// Many distinct tenants coexist without a fixed upper bound.
	for i := 0; i < 1000; i++ {
		tenant := "tenant-" + string(rune('A'+i/26)) + string(rune('a'+i%26))
		if ok, _ := l.Allow(tenant, 1, now); !ok {
			t.Fatalf("tenant %q rejected", tenant)
		}
	}
	if l.ActiveTenants() != 1002 {
		t.Fatalf("active=%d, want 1002", l.ActiveTenants())
	}
}

func TestUnknownTenantReportedFull(t *testing.T) {
	l := newTestLimiter(t, 4, 1)
	got, err := l.Available("ghost", time.Unix(0, 0))
	if err != nil || got != 4 {
		t.Fatalf("unknown tenant: %d err=%v, want 4", got, err)
	}
	if l.ActiveTenants() != 0 {
		t.Fatalf("query must not create a tenant, active=%d", l.ActiveTenants())
	}
}

func TestReclaimIdleRestartsFull(t *testing.T) {
	l := newTestLimiter(t, 5, 1)
	t0 := time.Unix(1000, 0)

	// Active tenant "a" at t0 is drained.
	if _, err := l.Allow("a", 5, t0); err != nil {
		t.Fatal(err)
	}
	// Idle tenant "b" was last seen much earlier.
	tOld := t0.Add(-time.Hour)
	if _, err := l.Allow("b", 1, tOld); err != nil {
		t.Fatal(err)
	}
	// "c" stays current.
	if _, err := l.Allow("c", 1, t0); err != nil {
		t.Fatal(err)
	}

	n := l.ReclaimIdle(t0) // strictly before t0 means: only b
	if n != 1 {
		t.Fatalf("reclaimed=%d, want 1", n)
	}
	if l.ActiveTenants() != 2 {
		t.Fatalf("active=%d, want 2", l.ActiveTenants())
	}

	// "b" reappears: it must start over with a full bucket.
	if ok, err := l.Allow("b", 5, t0); !ok || err != nil {
		t.Fatalf("recreated b: ok=%v err=%v", ok, err)
	}
	// "a" was not reclaimed; it is still empty at t0.
	got, _ := l.Available("a", t0)
	if got != 0 {
		t.Fatalf("a=%d, want 0 (must survive reclaim)", got)
	}
}

func TestReclaimWithCutoffInFuture(t *testing.T) {
	l := newTestLimiter(t, 5, 1)
	now := time.Unix(0, 0)
	_, _ = l.Allow("a", 1, now)
	_, _ = l.Allow("b", 1, now)
	if n := l.ReclaimIdle(now.Add(time.Nanosecond)); n != 2 {
		t.Fatalf("reclaimed=%d, want 2", n)
	}
	if l.ActiveTenants() != 0 {
		t.Fatal("all tenants should be gone")
	}
}

func TestNewLimiterValidatesConfig(t *testing.T) {
	if _, err := NewLimiter(Config{Capacity: 0, Rate: 1}); !errors.Is(err, ErrInvalidCapacity) {
		t.Fatalf("cap 0: %v", err)
	}
	if _, err := NewLimiter(Config{Capacity: 1, Rate: 0}); !errors.Is(err, ErrInvalidRate) {
		t.Fatalf("rate 0: %v", err)
	}
}
