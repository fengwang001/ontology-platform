package ontology

import "testing"
import "time"

func TestTenantsAreIsolated(t *testing.T) {
	base := time.Unix(900, 0)
	limiter := testLimiter(t, Config{Rate: 5, Capacity: 3})

	assertNoError(t, limiter.Allow("a", 3, base))
	assertNoError(t, limiter.Allow("b", 3, base))
	assertAvailable(t, limiter, "a", base, 0)

	assertNoError(t, limiter.Allow("b", 1, base.Add(time.Second)))
	assertAvailable(t, limiter, "a", base.Add(time.Second), 3)
	assertAvailable(t, limiter, "b", base.Add(time.Second), 2)

	assertAvailable(t, limiter, "a", base.Add(2*time.Second), 3)
	assertAvailable(t, limiter, "b", base.Add(2*time.Second), 3)
}

func TestInactiveTenantIsEvictedAndRestartsFull(t *testing.T) {
	base := time.Unix(1000, 0)
	limiter := testLimiter(t, Config{Rate: 5, Capacity: 3, IdleTTL: time.Second})

	assertNoError(t, limiter.Allow("idle", 3, base))
	assertNoError(t, limiter.Allow("busy", 3, base.Add(time.Nanosecond)))
	if got := limiter.ActiveTenants(); got != 2 {
		t.Fatalf("ActiveTenants() = %d, want 2", got)
	}

	later := base.Add(time.Second + time.Nanosecond)
	limiter.EvictInactive(later)
	if got := limiter.ActiveTenants(); got != 1 {
		t.Fatalf("ActiveTenants() after eviction = %d, want 1", got)
	}

	assertAvailable(t, limiter, "idle", later, 3)
	if got := limiter.ActiveTenants(); got != 2 {
		t.Fatalf("ActiveTenants() after recreation = %d, want 2", got)
	}
}

func TestUnknownTenantReportsFullBucket(t *testing.T) {
	now := time.Unix(1100, 0)
	limiter := testLimiter(t, Config{Rate: 5, Capacity: 4})
	assertAvailable(t, limiter, "new", now, 4)
}
