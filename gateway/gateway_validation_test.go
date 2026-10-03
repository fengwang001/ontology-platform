package gateway_test

import (
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/gateway"
)

// TestAddTenantValidation covers invalid params and duplicates.
func TestAddTenantValidation(t *testing.T) {
	g := mustGW(t, 100)
	cases := []struct {
		name       string
		rate, burs int64
		maxP       int
	}{
		{"", 1, 1, 0},
		{strings.Repeat("x", 65), 1, 1, 0},
		{"X", -1, 1, 0},
		{"X", 1_000_000_001, 1, 0},
		{"X", 1, 0, 0},
		{"X", 1, 1, -1},
		{"X", 1, 1, 3},
	}
	for _, c := range cases {
		if err := g.AddTenant(c.name, c.rate, c.burs, c.maxP); !errors.Is(err, gateway.ErrInvalid) {
			t.Fatalf("case %+v: want ErrInvalid, got %v", c, err)
		}
	}
	if err := g.AddTenant("dup", 1, 1, 0); err != nil {
		t.Fatal(err)
	}
	if err := g.AddTenant("dup", 1, 1, 0); !errors.Is(err, gateway.ErrDuplicate) {
		t.Fatalf("dup: want ErrDuplicate, got %v", err)
	}
	if _, err := gateway.New(0); !errors.Is(err, gateway.ErrInvalid) {
		t.Fatalf("cap 0: want ErrInvalid, got %v", err)
	}
	if _, err := gateway.New(1_000_000_001); !errors.Is(err, gateway.ErrInvalid) {
		t.Fatalf("cap too big: want ErrInvalid, got %v", err)
	}
}

// TestRejectOrder verifies first-error ordering: invalid, clock, tenant,
// forbidden, quota.
func TestRejectOrder(t *testing.T) {
	g := mustGW(t, 100)
	g.AddTenant("A", 0, 1, 2)
	if _, err := g.Ingest(10, "A", 2, 0); !errors.Is(err, gateway.ErrInvalid) {
		t.Fatalf("invalid size: %v", err)
	}
	g.Ingest(10, "A", 2, 1)
	if _, err := g.Ingest(5, "A", 2, 1); !errors.Is(err, gateway.ErrClockBackward) {
		t.Fatalf("clock: %v", err)
	}
	if _, err := g.Ingest(10, "Z", 2, 1); !errors.Is(err, gateway.ErrNoTenant) {
		t.Fatalf("tenant: %v", err)
	}
	if _, err := g.Ingest(10, "A", 0, 1); !errors.Is(err, gateway.ErrForbidden) {
		t.Fatalf("forbidden: %v", err)
	}
	// A spent its only burst byte and has rate 0: quota fails before queue.
	if _, err := g.Ingest(10, "A", 2, 1); !errors.Is(err, gateway.ErrQuota) {
		t.Fatalf("quota: %v", err)
	}
	if _, err := g.Drain(1, -1); !errors.Is(err, gateway.ErrInvalid) {
		t.Fatalf("drain invalid: %v", err)
	}
}

// TestSameTenantEvictionNoRefundInQuota ensures the refund from an eviction
// of the ingesting tenant itself is not counted in the quota decision.
func TestSameTenantEvictionNoRefundInQuota(t *testing.T) {
	g := mustGW(t, 100)
	g.AddTenant("A", 0, 60, 0)
	g.Ingest(0, "A", 2, 60) // tokens 0
	if _, err := g.Ingest(1, "A", 0, 60); !errors.Is(err, gateway.ErrQuota) {
		t.Fatalf("same-tenant refund counted in quota: %v", err)
	}
	if g.Used() != 60 {
		t.Fatalf("queue changed after quota reject: used=%d", g.Used())
	}

	// Refund clamps at the cap: A refills to 100000 via rate*delta, then
	// adding back 40000 stays at 100000.
	g2 := mustGW(t, 160)
	g2.AddTenant("A", 100, 100, 2)
	g2.AddTenant("B", 100, 100, 0)
	g2.Ingest(0, "A", 2, 40)
	g2.Ingest(0, "B", 1, 60)
	res, err := g2.Ingest(1000, "B", 0, 100) // free 60, need 40: evict A
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Evicted) != 1 || res.Evicted[0].Tenant != "A" {
		t.Fatalf("evicted = %+v", res.Evicted)
	}
	if tok, _ := g2.TokenBalance("A"); tok != 100000 {
		t.Fatalf("A tokens = %d, want 100000 (clamped)", tok)
	}
	if tok, _ := g2.TokenBalance("B"); tok != 0 {
		t.Fatalf("B tokens = %d, want 0", tok)
	}
}

// TestConcurrentIngestDrain exercises concurrent Ingest and Drain under the
// race detector; timestamps come from a monotonic allocator, and callers
// retry on clock-backward serialization noise, then invariants are checked.
func TestConcurrentIngestDrain(t *testing.T) {
	g, _ := gateway.New(1000)
	tenants := []string{"A", "B", "C", "D"}
	maxP := []int{0, 1, 1, 2}
	for i, n := range tenants {
		if err := g.AddTenant(n, 500, 1000, maxP[i]); err != nil {
			t.Fatal(err)
		}
	}
	var clock int64
	nextNow := func() int64 { return atomic.AddInt64(&clock, 1) }
	var wg sync.WaitGroup
	for w := 0; w < 6; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for k := 0; k < 500; k++ {
				idx := (id + k) % len(tenants)
				prio := maxP[idx] + ((id + k) % 2)
				if prio > 2 {
					prio = 2
				}
				size := int64(1 + (id*7+k*13)%200)
				for {
					if _, err := g.Ingest(nextNow(), tenants[idx], prio, size); !errors.Is(err, gateway.ErrClockBackward) {
						break
					}
				}
			}
		}(w)
	}
	for w := 0; w < 3; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for k := 0; k < 500; k++ {
				budget := int64(50 + (id+k)%300)
				for {
					if _, err := g.Drain(nextNow(), budget); !errors.Is(err, gateway.ErrClockBackward) {
						if err != nil {
							t.Errorf("drain: %v", err)
							return
						}
						break
					}
				}
			}
		}(w)
	}
	wg.Wait()

	if used := g.Used(); used < 0 || used > g.Cap() {
		t.Fatalf("used out of bounds: %d", used)
	}
	stats := g.Stats()
	var queued int64
	for name, s := range stats {
		for p := 0; p < 3; p++ {
			if s.AcceptedBytes[p] != s.EvictedBytes[p]+s.DrainedBytes[p]+s.QueuedBytes[p] {
				t.Fatalf("%s prio %d invariant broken", name, p)
			}
			queued += s.QueuedBytes[p]
		}
	}
	if queued != g.Used() {
		t.Fatalf("sum queued %d != used %d", queued, g.Used())
	}
}
