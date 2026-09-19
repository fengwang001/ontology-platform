package ontology

import (
	"testing"
	"time"
)

func TestEvictionRestartsInactiveTenantWithFullBucket(t *testing.T) {
	start := time.Unix(0, 0)
	limiter := NewLimiter(4, 4)

	if _, err := limiter.Allow("active", 1, start); err != nil {
		t.Fatal(err)
	}
	if _, err := limiter.Allow("idle", 4, start); err != nil {
		t.Fatal(err)
	}
	if got := limiter.ActiveTenants(); got != 2 {
		t.Fatalf("active tenants = %d, want 2", got)
	}

	now := start.Add(30 * time.Second)
	if _, err := limiter.Available("active", now); err != nil {
		t.Fatal(err)
	}
	removed := limiter.EvictIdle(30*time.Second, now)
	if removed != 1 {
		t.Fatalf("removed = %d, want 1", removed)
	}
	if got := limiter.ActiveTenants(); got != 1 {
		t.Fatalf("active tenants after eviction = %d, want 1", got)
	}
	if got, err := limiter.Available("idle", now); err != nil || got != 4 {
		t.Fatalf("recreated idle tenant = %d, want 4", got)
	}
}

func TestZeroIdlenessKeepsRecentlyUsedTenant(t *testing.T) {
	now := time.Unix(0, 0)
	limiter := NewLimiter(2, 2)

	if _, err := limiter.Allow("tenant", 2, now); err != nil {
		t.Fatal(err)
	}
	if removed := limiter.EvictIdle(1, now); removed != 0 {
		t.Fatalf("removed = %d, want 0", removed)
	}
	if got, err := limiter.Available("tenant", now); err != nil || got != 0 {
		t.Fatalf("active tenant = %d, want 0", got)
	}
}
