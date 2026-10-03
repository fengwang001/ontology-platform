package gateway_test

import (
	"errors"
	"testing"

	"ontology/gateway"
)

func mustGW(t *testing.T, cap int64) *gateway.Gateway {
	t.Helper()
	g, err := gateway.New(cap)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return g
}

// TestSpecExample replays the exact worked example from the specification.
func TestSpecExample(t *testing.T) {
	g := mustGW(t, 100)
	if err := g.AddTenant("A", 10, 100, 1); err != nil {
		t.Fatal(err)
	}
	if err := g.AddTenant("B", 10, 100, 0); err != nil {
		t.Fatal(err)
	}

	ing := func(now int64, name string, prio int, size int64) gateway.IngressResult {
		t.Helper()
		res, err := g.Ingest(now, name, prio, size)
		if err != nil {
			t.Fatalf("Ingest(%d,%s,%d,%d): %v", now, name, prio, size, err)
		}
		return res
	}

	ing(0, "A", 2, 60)
	if tok, _ := g.TokenBalance("A"); tok != 40000 {
		t.Fatalf("A tokens = %d, want 40000", tok)
	}
	if g.Used() != 60 {
		t.Fatalf("used = %d, want 60", g.Used())
	}

	ing(0, "B", 1, 30)
	if tok, _ := g.TokenBalance("B"); tok != 70000 {
		t.Fatalf("B tokens = %d, want 70000", tok)
	}
	if g.Used() != 90 {
		t.Fatalf("used = %d, want 90", g.Used())
	}

	res := ing(0, "B", 0, 30)
	if len(res.Evicted) != 1 || res.Evicted[0].Tenant != "A" || res.Evicted[0].Size != 60 {
		t.Fatalf("evicted = %+v", res.Evicted)
	}
	if tok, _ := g.TokenBalance("A"); tok != 100000 {
		t.Fatalf("A tokens after refund = %d, want 100000", tok)
	}
	if tok, _ := g.TokenBalance("B"); tok != 40000 {
		t.Fatalf("B tokens after debit = %d, want 40000", tok)
	}
	if g.Used() != 60 {
		t.Fatalf("used = %d, want 60", g.Used())
	}

	recs := g.Snapshot()
	if len(recs) != 2 || recs[0].Prio != 0 || recs[1].Prio != 1 {
		t.Fatalf("snapshot = %+v", recs)
	}

	out, err := g.Drain(1, 40)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || out[0].Size != 30 || out[0].Prio != 0 {
		t.Fatalf("drain = %+v", out)
	}
	if g.Used() != 30 {
		t.Fatalf("used after drain = %d, want 30", g.Used())
	}

	// A at t=2: virtual balance capped at 100000 is sufficient, but
	// free=70 < size=80 and nothing lower-priority is queued: queue full and
	// A's tokens and lastRefill must be untouched.
	if _, err := g.Ingest(2, "A", 2, 80); !errors.Is(err, gateway.ErrQueueFull) {
		t.Fatalf("want ErrQueueFull, got %v", err)
	}
	if tok, _ := g.TokenBalance("A"); tok != 100000 {
		t.Fatalf("A tokens changed on full queue: %d", tok)
	}
	if lr, _ := g.LastRefill("A"); lr != 0 {
		t.Fatalf("A lastRefill changed on full queue: %d", lr)
	}

	// A using prio 0 is forbidden (maxPrio 1), no silent downgrade.
	if _, err := g.Ingest(2, "A", 0, 1); !errors.Is(err, gateway.ErrForbidden) {
		t.Fatalf("want ErrForbidden, got %v", err)
	}

	// B at t=2: virtual 40000 + 10*2 = 40020 < 50000: quota short; the
	// queue capacity must not even be consulted (it would also be full).
	if _, err := g.Ingest(2, "B", 1, 50); !errors.Is(err, gateway.ErrQuota) {
		t.Fatalf("want ErrQuota, got %v", err)
	}

	// B at t=1000: virtual 40000 + 10*1000 = 50000 exactly: admitted.
	if _, err := g.Ingest(1000, "B", 1, 50); err != nil {
		t.Fatalf("exact-equality ingest: %v", err)
	}
	if tok, _ := g.TokenBalance("B"); tok != 0 {
		t.Fatalf("B tokens = %d, want 0", tok)
	}

	// One millibyte less in balance fails (independent gateway so the queue
	// boundary cannot interfere with the quota boundary).
	g2 := mustGW(t, 200)
	g2.AddTenant("T", 10, 100, 0)
	if _, err := g2.Ingest(0, "T", 0, 100); err != nil {
		t.Fatal(err)
	}
	// Tokens are 0; at t=99 the refill adds exactly 990 millibytes, one
	// short of the 1000 cost.
	if _, err := g2.Ingest(99, "T", 0, 1); !errors.Is(err, gateway.ErrQuota) {
		t.Fatalf("one millibyte short: want ErrQuota, got %v", err)
	}
	// The rejected t=99 call must not have settled any refill.
	if lr, _ := g2.LastRefill("T"); lr != 0 {
		t.Fatalf("lastRefill = %d, want 0 after rejected quota call", lr)
	}
	if _, err := g2.Ingest(100, "T", 0, 1); err != nil {
		t.Fatalf("exact refill equality: %v", err)
	}
}

// TestStatsInvariant verifies accepted == evicted + drained + queued for
// every tenant and priority after a mixed replay.
func TestStatsInvariant(t *testing.T) {
	g := mustGW(t, 40)
	g.AddTenant("A", 5, 40, 2)
	g.AddTenant("B", 20, 40, 0)

	g.Ingest(0, "A", 2, 20)
	g.Ingest(0, "B", 1, 15)
	g.Ingest(0, "B", 0, 20) // evicts A's 20
	g.Drain(1, 25)

	stats := g.Stats()
	for name, s := range stats {
		for p := 0; p < 3; p++ {
			if s.AcceptedBytes[p] != s.EvictedBytes[p]+s.DrainedBytes[p]+s.QueuedBytes[p] {
				t.Fatalf("%s prio %d: %d != %d+%d+%d", name, p,
					s.AcceptedBytes[p], s.EvictedBytes[p],
					s.DrainedBytes[p], s.QueuedBytes[p])
			}
		}
	}
	if stats["A"].EvictedBytes[2] != 20 ||
		stats["B"].DrainedBytes[0] != 20 || stats["B"].QueuedBytes[1] != 15 {
		t.Fatalf("unexpected stats: %+v", stats)
	}
}
