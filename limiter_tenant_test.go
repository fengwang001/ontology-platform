package ontology

import (
	"errors"
	"testing"
	"time"
)

func TestTenantsAreIsolated(t *testing.T) {
	base := time.Unix(0, 0)
	l := NewLimiter(4)
	for _, tenant := range []string{"a", "b"} {
		if err := l.AddTenant(tenant, Config{Capacity: 3, Rate: 1}, base); err != nil {
			t.Fatal(err)
		}
	}

	if ok, err := l.Allow("a", 3, base); !ok || err != nil {
		t.Fatal(err)
	}
	gotA, err := l.AvailableTokens("a", base)
	if err != nil {
		t.Fatal(err)
	}
	gotB, err := l.AvailableTokens("b", base)
	if err != nil {
		t.Fatal(err)
	}
	if gotA != 0 || gotB != 3 {
		t.Fatalf("tenant isolation failed: a=%d b=%d", gotA, gotB)
	}

	gotA, err = l.AvailableTokens("a", base.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	gotB, err = l.AvailableTokens("b", base.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if gotA != 2 || gotB != 3 {
		t.Fatalf("refill isolation failed: a=%d b=%d", gotA, gotB)
	}
}

func TestReapInactiveAndFullRestart(t *testing.T) {
	base := time.Unix(0, 0)
	l := NewLimiter(4)
	if err := l.AddTenant("active", Config{Capacity: 1, Rate: 1}, base); err != nil {
		t.Fatal(err)
	}
	if err := l.AddTenant("idle", Config{Capacity: 1, Rate: 1}, base); err != nil {
		t.Fatal(err)
	}
	if count := l.ActiveTenants(); count != 2 {
		t.Fatalf("active count = %d, want 2", count)
	}

	now := base.Add(time.Minute)
	if ok, err := l.Allow("active", 1, now); !ok || err != nil {
		t.Fatal(err)
	}
	if count := l.ReapInactive(now); count != 1 {
		t.Fatalf("reaped = %d, want 1", count)
	}
	if count := l.ActiveTenants(); count != 1 {
		t.Fatalf("active after reap = %d, want 1", count)
	}
	if _, err := l.AvailableTokens("idle", now); !errors.Is(err, ErrUnknownTenant) {
		t.Fatalf("reaped tenant query = %v", err)
	}

	if err := l.AddTenant("idle", Config{Capacity: 1, Rate: 1}, now); err != nil {
		t.Fatal(err)
	}
	if ok, err := l.Allow("idle", 1, now); !ok || err != nil {
		t.Fatalf("restart should be full: (%v, %v)", ok, err)
	}
}

func TestReapIsStrictlyBeforeCutoff(t *testing.T) {
	base := time.Unix(0, 0)
	l := NewLimiter(1)
	if err := l.AddTenant("t", Config{Capacity: 1, Rate: 1}, base); err != nil {
		t.Fatal(err)
	}
	if count := l.ReapInactive(base); count != 0 {
		t.Fatalf("reap at equal cutoff = %d, want 0", count)
	}
}

func TestInvalidConfigAndDuplicateTenant(t *testing.T) {
	base := time.Unix(0, 0)
	l := NewLimiter(1)
	if err := l.AddTenant("bad", Config{Capacity: 0, Rate: 1}, base); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("zero capacity = %v", err)
	}
	if err := l.AddTenant("bad", Config{Capacity: 1, Rate: 0}, base); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("zero rate = %v", err)
	}
	if err := l.AddTenant("t", Config{Capacity: 1, Rate: 1}, base); err != nil {
		t.Fatal(err)
	}
	if err := l.AddTenant("t", Config{Capacity: 1, Rate: 1}, base); !errors.Is(err, ErrTenantAlreadyExists) {
		t.Fatalf("duplicate = %v", err)
	}
}
