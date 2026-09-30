package lease

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

func p(v int64) *int64 { return &v }

func newTest(t *testing.T, low, high, L, H, Q int64) (*Server, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	srv := New(Config{
		Low: low, High: high,
		LeaseTime: L, ReserveTime: H, Quarantine: Q,
		Logger: DefaultLogger(&buf),
	})
	return srv, &buf
}

func mustDiscover(t *testing.T, srv *Server, now int64, client string, req *int64) int64 {
	t.Helper()
	addr, err := srv.Discover(now, client, req)
	if err != nil {
		t.Fatalf("Discover(%q,%v): unexpected error %v", client, req, err)
	}
	return addr
}

// A client returning to its historical address wins over the requested
// address (rule 2 precedes rule 3).
func TestDiscover_LastHeldBeatsRequested(t *testing.T) {
	srv, _ := newTest(t, 10, 12, 100, 10, 50)

	addr := mustDiscover(t, srv, 0, "a", p(12))
	if addr != 12 {
		t.Fatalf("first discover = %d, want 12", addr)
	}
	if err := srv.Confirm(1, "a", 12); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	if err := srv.Release(5, "a", 12); err != nil {
		t.Fatalf("release: %v", err)
	}

	got := mustDiscover(t, srv, 6, "a", p(10))
	if got != 12 {
		t.Fatalf("returning client = %d, want last-held 12 (rule 2 > rule 3)", got)
	}
}

// Rule 4 selects by earliest lease termination time; addresses never leased
// count as earliest; ties go to the smaller address.
func TestDiscover_EarliestTermination(t *testing.T) {
	srv, _ := newTest(t, 0, 3, 100, 10, 50)

	// Address 0: lease [0,100) expires at 100 -> termination time 100.
	if a1 := mustDiscover(t, srv, 0, "c1", nil); a1 != 0 {
		t.Fatalf("c1 = %d, want 0 (never leased, smallest)", a1)
	}
	if err := srv.Confirm(0, "c1", 0); err != nil {
		t.Fatal(err)
	}

	// Address 1: released at 40 -> termination time 40.
	if mustDiscover(t, srv, 1, "c2", nil) != 1 {
		t.Fatal("c2 should get address 1")
	}
	if err := srv.Confirm(1, "c2", 1); err != nil {
		t.Fatal(err)
	}
	if err := srv.Release(40, "c2", 1); err != nil {
		t.Fatal(err)
	}

	// Free at t=50: addr1 terminated 40, addrs 2 and 3 never leased.
	// Never-leased ranks earliest; 2 < 3.
	if got := mustDiscover(t, srv, 50, "c3", nil); got != 2 {
		t.Fatalf("c3 = %d, want 2 (never leased beats terminated 40)", got)
	}
	if got := mustDiscover(t, srv, 51, "c4", nil); got != 3 {
		t.Fatalf("c4 = %d, want 3 (remaining never-leased)", got)
	}

	// c3 and c4 confirm their addresses so they stay leased long enough.
	if err := srv.Confirm(53, "c3", 2); err != nil {
		t.Fatal(err)
	}
	if err := srv.Confirm(54, "c4", 3); err != nil {
		t.Fatal(err)
	}

	// Only address 1 is selectable: address 0's lease is valid until 100,
	// 2 and 3 until 153/154. The release time 40 beats expiry end 100.
	if got := mustDiscover(t, srv, 55, "c5", nil); got != 1 {
		t.Fatalf("c5 = %d, want 1 (released at 40 beats lease ending 100)", got)
	}
	if err := srv.Confirm(56, "c5", 1); err != nil {
		t.Fatal(err)
	}

	// Address 0 expires exactly at 100; at 101 it is free with termination
	// time 100 (the lease end), whereas 1's termination is the release time
	// 40 — but 1 is leased again by c5, so c6 takes the expired address 0.
	if got := mustDiscover(t, srv, 101, "c6", nil); got != 0 {
		t.Fatalf("c6 = %d, want 0 (lease [0,100) expired at its end 100)", got)
	}

	// White-box check of the two distinct termination-time meanings.
	srv.mu.Lock()
	if t0 := srv.addrs[0].terminatedAt; t0 != 100 || !srv.addrs[0].everLeased {
		t.Errorf("addr 0 terminatedAt = %d, want 100 (lease expiry end)", t0)
	}
	if t1 := srv.addrs[1].terminatedAt; t1 != 40 {
		t.Errorf("addr 1 terminatedAt = %d, want 40 (release time)", t1)
	}
	srv.mu.Unlock()
}

// A lease [t,t+L) is valid at t+L-1 and invalid exactly at t+L.
func TestLease_ExpiresExactlyAtEnd(t *testing.T) {
	srv, _ := newTest(t, 0, 0, 10, 100, 5)
	addr := mustDiscover(t, srv, 0, "a", nil)
	if err := srv.Confirm(0, "a", addr); err != nil {
		t.Fatal(err)
	}
	if err := srv.Release(9, "b", addr); !errors.Is(err, ErrNotHolder) {
		t.Fatalf("release at 9 by b: %v, want ErrNotHolder", err)
	}
	if err := srv.Release(10, "a", addr); !errors.Is(err, ErrNotHolder) {
		t.Fatalf("release at 10 by a: %v, want ErrNotHolder (lease expired)", err)
	}
	if err := srv.Confirm(10, "a", addr); !errors.Is(err, ErrNoReservation) {
		t.Fatalf("confirm at 10: %v, want ErrNoReservation", err)
	}
}

// Renewal starts a fresh lease at "now"; residual time is never stacked.
func TestConfirm_RenewalDoesNotStack(t *testing.T) {
	srv, _ := newTest(t, 0, 0, 10, 100, 5)
	addr := mustDiscover(t, srv, 0, "a", nil)
	if err := srv.Confirm(0, "a", addr); err != nil {
		t.Fatal(err)
	}
	if err := srv.Confirm(8, "a", addr); err != nil {
		t.Fatalf("renew: %v", err)
	}
	if err := srv.Release(17, "b", addr); !errors.Is(err, ErrNotHolder) {
		t.Fatalf("at 17 holder check: %v", err)
	}
	// Renewed lease ends at 18 (8+10), not 20 (8+12 stacked).
	if err := srv.Release(18, "a", addr); !errors.Is(err, ErrNotHolder) {
		t.Fatalf("at 18 lease should be expired; got %v", err)
	}
}

// A rejected address is quarantined for Q and unselectable until the end.
func TestReject_Quarantine(t *testing.T) {
	srv, _ := newTest(t, 0, 1, 100, 100, 30)
	addr := mustDiscover(t, srv, 0, "a", nil)
	if err := srv.Confirm(0, "a", addr); err != nil {
		t.Fatal(err)
	}
	if err := srv.Reject(10, "a", addr); err != nil {
		t.Fatalf("reject: %v", err)
	}

	if got := mustDiscover(t, srv, 20, "b", p(0)); got != 1 {
		t.Fatalf("b = %d, want 1 (0 quarantined [10,40))", got)
	}
	if _, err := srv.Discover(39, "c", nil); !errors.Is(err, ErrPoolExhausted) {
		t.Fatalf("at 39: %v, want ErrPoolExhausted", err)
	}
	// Quarantine is half-open: at 40 address 0 is free.
	if got := mustDiscover(t, srv, 40, "d", nil); got != 0 {
		t.Fatalf("d = %d, want 0 (quarantine ended at 40)", got)
	}
}

// An expired reservation can be taken by another client; repeat Discover for
// the same client never refreshes the reservation start.
func TestReservation_ExpiryAndBoundaries(t *testing.T) {
	srv, _ := newTest(t, 0, 0, 100, 10, 50)
	if got := mustDiscover(t, srv, 0, "a", nil); got != 0 {
		t.Fatalf("a = %d, want 0", got)
	}
	if _, err := srv.Discover(5, "b", p(0)); !errors.Is(err, ErrPoolExhausted) {
		t.Fatalf("b while reserved: %v, want ErrPoolExhausted", err)
	}
	if got := mustDiscover(t, srv, 9, "a", nil); got != 0 {
		t.Fatalf("repeated discover = %d, want 0", got)
	}
	if got := mustDiscover(t, srv, 10, "b", p(0)); got != 0 {
		t.Fatalf("b at 10 = %d, want 0 (reservation [0,10) expired)", got)
	}

	// Reserver rejection quarantines without inventing a lease termination.
	srv2, _ := newTest(t, 0, 1, 100, 100, 10)
	mustDiscover(t, srv2, 0, "a", p(0))
	if err := srv2.Reject(5, "a", 0); err != nil {
		t.Fatalf("reject reservation: %v", err)
	}
	if got := mustDiscover(t, srv2, 6, "b", nil); got != 1 {
		t.Fatalf("b = %d, want 1 (0 quarantined after reservation rejection)", got)
	}
}

// Acceptance and rejection rules for Confirm / Release / Reject.
func TestConfirm_Release_Reject_Rules(t *testing.T) {
	srv, _ := newTest(t, 0, 2, 100, 10, 20)
	a := mustDiscover(t, srv, 0, "a", nil)

	if err := srv.Confirm(1, "b", a); !errors.Is(err, ErrHeldByOther) {
		t.Fatalf("b confirm reserved addr: %v, want ErrHeldByOther", err)
	}
	if err := srv.Confirm(1, "a", 1); !errors.Is(err, ErrNoReservation) {
		t.Fatalf("a confirm free addr: %v, want ErrNoReservation", err)
	}
	if err := srv.Confirm(1, "a", 9); !errors.Is(err, ErrAddressNotInPool) {
		t.Fatalf("confirm out of pool: %v, want ErrAddressNotInPool", err)
	}
	if err := srv.Confirm(1, "a", a); err != nil {
		t.Fatalf("confirm: %v", err)
	}

	if err := srv.Release(2, "b", a); !errors.Is(err, ErrNotHolder) {
		t.Fatalf("b release: %v, want ErrNotHolder", err)
	}

	b := mustDiscover(t, srv, 3, "b", nil)
	if err := srv.Reject(4, "c", b); !errors.Is(err, ErrNotHolder) {
		t.Fatalf("c reject b reservation: %v, want ErrNotHolder", err)
	}
	if err := srv.Reject(4, "b", b); err != nil {
		t.Fatalf("b reject own reservation: %v", err)
	}
}

// Rejection precedence; rejected operations must not mutate any state.
func TestRejectionPrecedenceAndAtomicity(t *testing.T) {
	srv, _ := newTest(t, 0, 0, 100, 10, 10)
	mustDiscover(t, srv, 10, "a", nil)

	if _, err := srv.Discover(9, "", p(99)); !errors.Is(err, ErrClockRolledBack) {
		t.Fatalf("discover precedence: %v, want ErrClockRolledBack", err)
	}
	if _, err := srv.Discover(10, "", p(99)); !errors.Is(err, ErrEmptyClient) {
		t.Fatalf("discover precedence: %v, want ErrEmptyClient", err)
	}
	if _, err := srv.Discover(10, "b", p(99)); !errors.Is(err, ErrAddressNotInPool) {
		t.Fatalf("discover precedence: %v, want ErrAddressNotInPool", err)
	}
	if _, err := srv.Discover(10, "b", nil); !errors.Is(err, ErrPoolExhausted) {
		t.Fatalf("discover precedence: %v, want ErrPoolExhausted", err)
	}

	if err := srv.Confirm(9, "a", 0); !errors.Is(err, ErrClockRolledBack) {
		t.Fatalf("confirm rollback: %v", err)
	}
	if err := srv.Confirm(10, "a", 5); !errors.Is(err, ErrAddressNotInPool) {
		t.Fatalf("confirm pool: %v", err)
	}
	if err := srv.Confirm(10, "b", 0); !errors.Is(err, ErrHeldByOther) {
		t.Fatalf("confirm held: %v, want ErrHeldByOther", err)
	}
	// After all the failed calls, a's reservation [10,20) is untouched.
	if err := srv.Confirm(11, "a", 0); err != nil {
		t.Fatalf("a confirm after failed ops: %v (state changed?)", err)
	}

	if err := srv.Release(10, "b", 5); !errors.Is(err, ErrClockRolledBack) {
		t.Fatalf("release precedence: %v, want ErrClockRolledBack", err)
	}
	if err := srv.Release(11, "b", 5); !errors.Is(err, ErrAddressNotInPool) {
		t.Fatalf("release precedence: %v, want ErrAddressNotInPool", err)
	}
	if err := srv.Release(11, "b", 0); !errors.Is(err, ErrNotHolder) {
		t.Fatalf("release precedence: %v, want ErrNotHolder", err)
	}
}

// Logs contain the input, output and the decision rationale.
func TestLoggingContent(t *testing.T) {
	srv, buf := newTest(t, 0, 1, 100, 10, 10)
	mustDiscover(t, srv, 0, "a", nil)
	if err := srv.Confirm(2, "a", 0); err != nil {
		t.Fatal(err)
	}
	log := buf.String()
	for _, want := range []string{
		"DISCOVER in={now:0 client:\"a\" requested:none}",
		"-> 0 OK (rule 4: never leased (earliest)",
		"CONFIRM in={now:2 client:\"a\" addr:0}",
		"-> OK (lease [2,102)",
	} {
		if !strings.Contains(log, want) {
			t.Fatalf("log missing %q in:\n%s", want, log)
		}
	}
}

// Replaying the identical operation sequence yields identical results.
func TestReplayDeterminism(t *testing.T) {
	type op struct {
		kind   string
		now    int64
		client string
		addr   int64
		hasReq bool
		req    int64
	}
	script := []op{
		{"discover", 0, "a", 0, true, 3},
		{"confirm", 1, "a", 3, false, 0},
		{"discover", 2, "b", 0, false, 0},
		{"release", 30, "a", 3, false, 0},
		{"discover", 31, "a", 0, true, 2},
		{"reject", 32, "a", 3, false, 0},
		{"discover", 33, "c", 0, false, 0},
		{"confirm", 34, "x", 1, false, 0},
		{"release", 10, "b", 0, false, 0},
	}
	run := func() string {
		srv, _ := newTest(t, 0, 3, 20, 5, 10)
		var sb strings.Builder
		for _, o := range script {
			switch o.kind {
			case "discover":
				var req *int64
				if o.hasReq {
					req = p(o.req)
				}
				addr, err := srv.Discover(o.now, o.client, req)
				fmt.Fprintf(&sb, "d %d %v\n", addr, err)
			case "confirm":
				fmt.Fprintf(&sb, "c %v\n", srv.Confirm(o.now, o.client, o.addr))
			case "release":
				fmt.Fprintf(&sb, "r %v\n", srv.Release(o.now, o.client, o.addr))
			case "reject":
				fmt.Fprintf(&sb, "j %v\n", srv.Reject(o.now, o.client, o.addr))
			}
		}
		return sb.String()
	}
	first := run()
	for i := 0; i < 3; i++ {
		if got := run(); got != first {
			t.Fatalf("replay %d differs:\n%s\nvs\n%s", i, got, first)
		}
	}
}

// Concurrent operations never put two live leases on one address and never
// let a lease coexist with another client's live reservation.
func TestConcurrentInvariants(t *testing.T) {
	srv, _ := newTest(t, 0, 7, 1000, 1000, 10)
	const clients = 40
	var wg sync.WaitGroup
	for i := 0; i < clients; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			client := string(rune('A'+i%26)) + "-" + string(rune('a'+i/26))
			for round := int64(0); round < 5; round++ {
				now := round * 10
				addr, err := srv.Discover(now, client, nil)
				if err != nil {
					continue
				}
				if err := srv.Confirm(now+1, client, addr); err == nil {
					if round%2 == 0 {
						_ = srv.Release(now+2, client, addr)
					}
				}
			}
		}(i)
	}
	wg.Wait()

	srv.mu.Lock()
	defer srv.mu.Unlock()
	byLease := map[int64]string{}
	byReserve := map[int64]string{}
	for addr, st := range srv.addrs {
		if st.leaseClient != "" {
			if other, dup := byLease[addr]; dup && other != st.leaseClient {
				t.Fatalf("address %d leased by both %q and %q", addr, other, st.leaseClient)
			}
			byLease[addr] = st.leaseClient
		}
		if st.reserveClient != "" {
			byReserve[addr] = st.reserveClient
		}
		if lc, rc := st.leaseClient, st.reserveClient; lc != "" && rc != "" && lc != rc {
			t.Fatalf("address %d lease by %q coexists with reservation by %q", addr, lc, rc)
		}
	}
}
