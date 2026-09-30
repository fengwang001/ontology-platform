package neighbor

import (
	"bytes"
	"log"
	"testing"
)

func testConfig() Config {
	// R=100, Dl=50, T=10, K=3, Q=2.
	return Config{ReachableTime: 100, DelayTime: 50, Retransmit: 10, MaxSends: 3, QueueLimit: 2}
}

func newTestCache(t *testing.T) (*Cache, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	c := New(testConfig(), log.New(&buf, "", 0))
	t.Cleanup(func() {
		t.Logf("neighbor cache log:\n%s", buf.String())
	})
	return c, &buf
}

func assertState(t *testing.T, c *Cache, addr Addr, want State) {
	t.Helper()
	got, ok := c.stateOf(addr)
	if !ok {
		t.Fatalf("entry %s missing, want state %s", addr, want)
	}
	if got != want {
		t.Fatalf("entry %s state = %s, want %s", addr, got, want)
	}
}

func packets(ds []Delivery) []any {
	out := make([]any, len(ds))
	for i, d := range ds {
		out[i] = d.Packet
	}
	return out
}

func assertPkts(t *testing.T, got []any, want ...any) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("packets = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("packets[%d]=%v, want %v (full %v)", i, got[i], want[i], got)
		}
	}
}

func assertPackets(t *testing.T, got []Packet, want ...any) {
	t.Helper()
	out := make([]any, len(got))
	for i, p := range got {
		out[i] = p
	}
	assertPkts(t, out, want...)
}

// Incomplete + solicited advertisement: queue flushes FIFO and goes Reachable.
func TestIncompleteSolicitedFlushesToReachable(t *testing.T) {
	c, _ := newTestCache(t)
	a := Addr("fe80::1")

	r, err := c.Send(0, a, "p1")
	if err != nil || len(r.Requests) != 1 || len(r.Delivered) != 0 {
		t.Fatalf("first send: %+v err=%v", r, err)
	}
	if r, _ := c.Send(1, a, "p2"); len(r.Delivered) != 0 || c.queuedOf(a) != 2 {
		t.Fatalf("second send should queue: %+v queued=%d", r, c.queuedOf(a))
	}
	assertState(t, c, a, Incomplete)

	r, err = c.Advertisement(2, a, "mac-A", true, true)
	if err != nil {
		t.Fatal(err)
	}
	assertPkts(t, packets(r.Delivered), "p1", "p2")
	for _, d := range r.Delivered {
		if d.Link != "mac-A" {
			t.Fatalf("delivered link = %q, want mac-A", d.Link)
		}
	}
	assertState(t, c, a, Reachable)
	if c.linkOf(a) != "mac-A" || c.queuedOf(a) != 0 {
		t.Fatalf("link=%q queued=%d", c.linkOf(a), c.queuedOf(a))
	}
}

// Incomplete + unsolicited advertisement: still flushes FIFO, but goes Stale.
func TestIncompleteUnsolicitedFlushesToStale(t *testing.T) {
	c, _ := newTestCache(t)
	a := Addr("fe80::2")

	if _, err := c.Send(0, a, "p1"); err != nil {
		t.Fatal(err)
	}
	r, err := c.Advertisement(1, a, "mac-B", false, true)
	if err != nil {
		t.Fatal(err)
	}
	assertPkts(t, packets(r.Delivered), "p1")
	assertState(t, c, a, Stale)

	// A stale entry must not be treated as reachable: sends go out but enter
	// Delay instead of refreshing the reachable timer.
	r, _ = c.Send(2, a, "p2")
	assertPkts(t, packets(r.Delivered), "p2")
	assertState(t, c, a, Delay)
}

// Boundary rule: now == deadline is due; Reachable becomes Stale.
func TestReachableExpiresExactlyAtBoundary(t *testing.T) {
	c, _ := newTestCache(t)
	a := Addr("fe80::3")

	c.Send(0, a, "p")
	c.Advertisement(0, a, "mac", true, true) // Reachable deadline = 0 + 100
	assertState(t, c, a, Reachable)

	// One tick before the deadline: still Reachable.
	if r, err := c.Tick(99); err != nil || len(r.Expired) != 0 {
		t.Fatalf("tick 99 should not expire: %+v err=%v", r, err)
	}
	assertState(t, c, a, Reachable)

	// Exactly on the deadline (now is not earlier than deadline): Stale.
	r, err := c.Tick(100)
	if err != nil || len(r.Expired) != 1 {
		t.Fatalf("tick 100: %+v err=%v", r, err)
	}
	if ex := r.Expired[0]; ex.From != Reachable || ex.To != Stale {
		t.Fatalf("expiry = %+v", ex)
	}
	assertState(t, c, a, Stale)
}

// Stale --send--> Delay --Dl--> Probe (probe #1 sent), then probes retry and
// the entry is deleted after K sends.
func TestDelayExpiresIntoProbeAndExhausts(t *testing.T) {
	c, _ := newTestCache(t)
	a := Addr("fe80::4")

	c.Send(0, a, "p")
	c.Advertisement(0, a, "mac", true, true)
	c.Tick(100) // Reachable -> Stale

	r, _ := c.Send(101, a, "p2") // Stale -> Delay, deadline 151
	assertState(t, c, a, Delay)
	assertPkts(t, packets(r.Delivered), "p2")

	r, _ = c.Tick(150) // one tick early: nothing
	if len(r.Expired) != 0 {
		t.Fatalf("tick 150 should not expire Delay: %+v", r)
	}
	r, _ = c.Tick(151) // Delay -> Probe, probe #1
	assertState(t, c, a, Probe)
	if len(r.Requests) != 1 || r.Requests[0] != a || !r.Expired[0].Request {
		t.Fatalf("expected probe #1: %+v", r)
	}

	// Probe timers: resend at 161 (#2), 171 (#3), then delete at 181.
	if r, _ = c.Tick(161); len(r.Requests) != 1 {
		t.Fatalf("probe #2: %+v", r)
	}
	if r, _ = c.Tick(171); len(r.Requests) != 1 {
		t.Fatalf("probe #3: %+v", r)
	}
	r, _ = c.Tick(181)
	if len(r.Expired) != 1 || !r.Expired[0].Deleted || c.Len() != 0 {
		t.Fatalf("probe entry should be deleted after K sends: %+v", r)
	}
}

// Upper-layer confirmation lifts Delay back to Reachable.
func TestConfirmMovesDelay(t *testing.T) {
	c, _ := newTestCache(t)
	a := Addr("fe80::5")
	c.Send(0, a, "p")
	c.Advertisement(0, a, "mac", true, true)
	c.Tick(100) // Stale
	c.Send(100, a, "p2")

	if _, err := c.Confirm(110, a); err != nil {
		t.Fatal(err)
	}
	assertState(t, c, a, Reachable)

	// Confirm while Reachable leaves the state unchanged.
	if _, err := c.Confirm(111, a); err != nil {
		t.Fatal(err)
	}
	assertState(t, c, a, Reachable)
}

// Incomplete retransmits K times total, then the entry and all queued packets
// are dropped as unreachable.
func TestIncompleteExhaustionDropsQueued(t *testing.T) {
	c, _ := newTestCache(t)
	a := Addr("fe80::6")
	c.Send(0, a, "p1") // request #1, next 10
	c.Send(1, a, "p2")

	if r, _ := c.Tick(10); len(r.Requests) != 1 {
		t.Fatalf("retry #2: %+v", r)
	}
	if r, _ := c.Tick(20); len(r.Requests) != 1 {
		t.Fatalf("retry #3: %+v", r)
	}
	r, _ := c.Tick(30) // K exhausted: delete, queued packets unreachable
	if len(r.DroppedUnreachable) != 2 || c.Len() != 0 {
		t.Fatalf("expected 2 unreachable drops and deletion: %+v len=%d", r, c.Len())
	}
	assertPackets(t, r.DroppedUnreachable, "p1", "p2")

	// The deleted address must resolve anew rather than be treated reachable.
	r, _ = c.Send(31, a, "p3")
	assertState(t, c, a, Incomplete)
	if len(r.Requests) != 1 || len(r.Delivered) != 0 {
		t.Fatalf("fresh resolution after deletion: %+v", r)
	}
}

// Queue overflow keeps only the Q newest packets; oldest is counted dropped.
func TestQueueOverflowDropsOldest(t *testing.T) {
	c, _ := newTestCache(t)
	a := Addr("fe80::7")
	c.Send(0, a, "p1")
	c.Send(1, a, "p2")
	r, _ := c.Send(2, a, "p3") // Q=2: p1 evicted
	assertPackets(t, r.DroppedOverflow, "p1")

	r, _ = c.Advertisement(3, a, "mac", true, true)
	assertPkts(t, packets(r.Delivered), "p2", "p3") // FIFO of survivors
}
