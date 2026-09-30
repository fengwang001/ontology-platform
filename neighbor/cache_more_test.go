package neighbor

import (
	"bytes"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"testing"
)

// Every queued packet is delivered exactly once in strict FIFO order.
func TestEveryPacketAccountedOnce(t *testing.T) {
	cfg := testConfig()
	cfg.QueueLimit = 1000
	cfg.Retransmit = 1_000_000 // no expiry while packets are still being queued
	var buf bytes.Buffer
	c := New(cfg, log.New(&buf, "", 0))
	a := Addr("fe80::8")

	const n = 50
	for i := 0; i < n; i++ {
		if r, err := c.Send(Time(i), a, i); err != nil {
			t.Fatal(err)
		} else if len(r.Delivered) != 0 {
			t.Fatalf("packet %d delivered before resolution", i)
		}
	}
	r, _ := c.Advertisement(n, a, "mac", true, true)
	if len(r.Delivered) != n {
		t.Fatalf("delivered %d, want %d", len(r.Delivered), n)
	}
	seen := map[any]bool{}
	for i, d := range r.Delivered {
		if d.Packet != i {
			t.Fatalf("position %d = %v, want strict FIFO", i, d.Packet)
		}
		if seen[d.Packet] {
			t.Fatalf("packet %v delivered twice", d.Packet)
		}
		seen[d.Packet] = true
	}
}

// Non-override advertisement with a different link-layer address: Reachable
// drops to Stale without recording; Stale/Delay/Probe ignore it entirely.
func TestNonOverrideDifferentLink(t *testing.T) {
	c, _ := newTestCache(t)
	a := Addr("fe80::9")
	c.Send(0, a, "p")
	c.Advertisement(0, a, "mac-1", true, true) // Reachable, mac-1

	r, err := c.Advertisement(50, a, "mac-2", false, false)
	if err != nil || len(r.Delivered) != 0 {
		t.Fatalf("non-override diff: %+v err=%v", r, err)
	}
	assertState(t, c, a, Stale)
	if c.linkOf(a) != "mac-1" {
		t.Fatalf("link recorded = %q, want mac-1 (must not update)", c.linkOf(a))
	}

	// From Stale the same advertisement is ignored.
	if _, _ = c.Advertisement(51, a, "mac-3", false, false); c.linkOf(a) != "mac-1" {
		t.Fatalf("stale link changed to %q", c.linkOf(a))
	}
	assertState(t, c, a, Stale)

	// Delay ignores it too.
	c.Send(52, a, "p2") // Stale -> Delay, deadline 102
	if _, _ = c.Advertisement(53, a, "mac-3", false, false); c.linkOf(a) != "mac-1" {
		t.Fatalf("delay link changed to %q", c.linkOf(a))
	}
	assertState(t, c, a, Delay)

	// Probe ignores it as well.
	c.Tick(102) // Delay -> Probe
	assertState(t, c, a, Probe)
	if _, _ = c.Advertisement(103, a, "mac-3", false, false); c.linkOf(a) != "mac-1" {
		t.Fatalf("probe link changed to %q", c.linkOf(a))
	}
	assertState(t, c, a, Probe)

	// Override with the new address is honored and solicited -> Reachable.
	if _, _ = c.Advertisement(104, a, "mac-3", true, true); c.linkOf(a) != "mac-3" {
		t.Fatalf("override link = %q, want mac-3", c.linkOf(a))
	}
	assertState(t, c, a, Reachable)
}

// Unsolicted advertisement with the same recorded address keeps state; a
// solicited advertisement refreshes any state to Reachable.
func TestAdvertisementAddressVariants(t *testing.T) {
	c, _ := newTestCache(t)
	a := Addr("fe80::10")
	c.Send(0, a, "p")
	c.Advertisement(0, a, "mac", true, true)
	c.Tick(100) // Stale

	// Unsolicited same-address, no override: ignored, stays Stale.
	if _, err := c.Advertisement(101, a, "mac", false, false); err != nil {
		t.Fatal(err)
	}
	assertState(t, c, a, Stale)

	// Solicited same-address even without override -> Reachable.
	if _, err := c.Advertisement(102, a, "mac", true, false); err != nil {
		t.Fatal(err)
	}
	assertState(t, c, a, Reachable)

	// Unsolicited override with a changed address -> Stale.
	if _, err := c.Advertisement(103, a, "mac2", false, true); err != nil {
		t.Fatal(err)
	}
	assertState(t, c, a, Stale)
	if c.linkOf(a) != "mac2" {
		t.Fatalf("link = %q, want mac2", c.linkOf(a))
	}
}

// Rejections report only the first error in the documented fixed order, and
// never mutate state beyond the expiry processing already completed.
func TestRejectionOrder(t *testing.T) {
	// 1) clock moved backward beats everything.
	c, _ := newTestCache(t)
	c.Tick(10)
	if _, err := c.Send(5, "", "p"); !errors.Is(err, ErrClockMovedBackward) {
		t.Fatalf("got %v, want clock error first", err)
	}
	// 2) empty address beats empty link / missing entry (at a non-rollback time).
	if _, err := c.Advertisement(10, "", "", false, false); !errors.Is(err, ErrEmptyAddr) {
		t.Fatalf("got %v, want empty addr", err)
	}
	if _, err := c.Confirm(10, ""); !errors.Is(err, ErrEmptyAddr) {
		t.Fatalf("got %v, want empty addr", err)
	}

	// 3) empty link beats missing entry.
	if _, err := c.Advertisement(10, "ghost", "", true, true); !errors.Is(err, ErrEmptyLinkAddr) {
		t.Fatalf("got %v, want empty link", err)
	}

	// 4) no entry.
	if _, err := c.Advertisement(10, "ghost", "mac", true, true); !errors.Is(err, ErrNoEntry) {
		t.Fatalf("got %v, want no entry", err)
	}
	if _, err := c.Confirm(10, "ghost"); !errors.Is(err, ErrNoEntry) {
		t.Fatalf("got %v, want no entry", err)
	}

	// 5) cache full on creation, evaluated after expiry (a deleted entry frees
	// the slot, so the send must succeed).
	cfg := testConfig()
	cfg.MaxEntries = 1
	c = New(cfg, nil)
	// Incomplete "a": request #1 at 0, #2 at 10, #3 at 20, deleted at 30.
	c.Send(0, "a", "p")
	c.Tick(10)
	c.Tick(20)
	if _, err := c.Send(20, "b", "p"); !errors.Is(err, ErrCacheFull) {
		t.Fatalf("got %v, want cache full", err)
	}
	r, err := c.Send(30, "b", "p") // expiry deletes "a" first
	if err != nil || len(r.Requests) != 1 || c.Len() != 1 {
		t.Fatalf("post-expiry send: %+v err=%v len=%d", r, err, c.Len())
	}
}

// Rejected operations leave entries, counters and queued packets untouched
// (expiry work that already completed still applies).
func TestRejectionHasNoSideEffects(t *testing.T) {
	c, _ := newTestCache(t)
	a := Addr("fe80::11")
	c.Send(0, a, "p1")

	if _, err := c.Send(1, a, nil); err != nil {
		t.Fatalf("nil packet is allowed, got %v", err)
	}
	before := c.queuedOf(a)
	if _, err := c.Advertisement(2, "", "mac", true, true); !errors.Is(err, ErrEmptyAddr) {
		t.Fatalf("got %v", err)
	}
	if _, err := c.Advertisement(2, a, "", true, true); !errors.Is(err, ErrEmptyLinkAddr) {
		t.Fatalf("got %v", err)
	}
	if c.queuedOf(a) != before {
		t.Fatalf("queue changed after rejected adv: %d -> %d", before, c.queuedOf(a))
	}
	assertState(t, c, a, Incomplete)
}

// Replaying the same operation sequence on two caches gives identical results.
type op struct {
	name string
	now  Time
	addr Addr
	pkt  any
	link LinkAddr
	sol  bool
	ovr  bool
}

func replay(t *testing.T, ops []op) string {
	t.Helper()
	var buf bytes.Buffer
	c := New(testConfig(), log.New(&buf, "", 0))
	for _, o := range ops {
		switch o.name {
		case "send":
			r, err := c.Send(o.now, o.addr, o.pkt)
			fmt.Fprintf(&buf, "send %v => %+v err=%v\n", o.pkt, r, err)
		case "adv":
			r, err := c.Advertisement(o.now, o.addr, o.link, o.sol, o.ovr)
			fmt.Fprintf(&buf, "adv %s => %+v err=%v\n", o.link, r, err)
		case "confirm":
			r, err := c.Confirm(o.now, o.addr)
			fmt.Fprintf(&buf, "confirm => %+v err=%v\n", r, err)
		case "tick":
			r, err := c.Tick(o.now)
			fmt.Fprintf(&buf, "tick => %+v err=%v\n", r, err)
		}
	}
	return buf.String()
}

func TestDeterministicReplay(t *testing.T) {
	ops := []op{
		{"send", 0, "a", 1, "", false, false},
		{"send", 1, "b", 2, "", false, false},
		{"send", 2, "a", 3, "", false, false},
		{"tick", 10, "", nil, "", false, false},
		{"adv", 11, "a", nil, "ma", true, true},
		{"tick", 20, "", nil, "", false, false},
		{"adv", 21, "b", nil, "mb", false, false},
		{"confirm", 22, "b", nil, "", false, false},
		{"send", 23, "a", 4, "", false, false},
	}
	first := replay(t, ops)
	second := replay(t, ops)
	if first != second {
		t.Fatalf("replays differ:\n%s----\n%s", first, second)
	}
}

// Concurrent operations must not race or corrupt the packet accounting.
func TestConcurrentOperations(t *testing.T) {
	cfg := testConfig()
	cfg.QueueLimit = 10000
	cfg.Retransmit = 1_000_000_000 // entries must not expire during the flood
	c := New(cfg, log.New(&bytes.Buffer{}, "", 0))
	a := Addr("conc")

	const writers = 16
	const per = 100
	var wg sync.WaitGroup
	var clock sync.Mutex // shared logical clock, always advanced forward
	now := Time(0)
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < per; i++ {
				// Allocate the timestamp and perform the operation as one
				// critical section so operations arrive in timestamp order.
				clock.Lock()
				now++
				if _, err := c.Send(now, a, id*per+i); err != nil {
					t.Errorf("concurrent send: %v", err)
				}
				clock.Unlock()
			}
		}(w)
	}
	wg.Wait()

	clock.Lock()
	now++
	r, err := c.Advertisement(now, a, "mac", true, true)
	clock.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Delivered) != writers*per {
		t.Fatalf("delivered %d, want %d", len(r.Delivered), writers*per)
	}
	seen := map[any]int{}
	for _, d := range r.Delivered {
		seen[d.Packet]++
	}
	for i := 0; i < writers*per; i++ {
		if seen[i] != 1 {
			t.Fatalf("packet %d count=%d (want exactly once)", i, seen[i])
		}
	}
}

// The diagnostic log prints input, output and the decision basis.
func TestLoggingContainsInputOutputBasis(t *testing.T) {
	var buf bytes.Buffer
	c := New(testConfig(), log.New(&buf, "", 0))
	c.Send(0, "log-a", "p")
	c.Tick(10)
	out := buf.String()
	for _, want := range []string{"[input]", "[output]", "[basis:", "SEND", "TICK", "retransmit"} {
		if !strings.Contains(out, want) {
			t.Fatalf("log missing %q:\n%s", want, out)
		}
	}
}
