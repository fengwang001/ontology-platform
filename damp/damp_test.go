package damp

import (
	"errors"
	"sync"
	"testing"
)

func newTestDamp(t *testing.T, delta, num, den, ps, pr, pmax, tmax int64) *Dampener {
	t.Helper()
	d, err := New(delta, num, den, ps, pr, pmax, tmax)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return d
}

func TestNewValidation(t *testing.T) {
	cases := []struct {
		name string
		args [7]int64
	}{
		{"delta low", [7]int64{0, 3, 4, 2000, 800, 6000, 200}},
		{"delta high", [7]int64{1_000_001, 3, 4, 2000, 800, 6000, 200}},
		{"pr not below ps", [7]int64{10, 3, 4, 2000, 2000, 6000, 200}},
		{"ps over pmax", [7]int64{10, 3, 4, 6001, 800, 6000, 200}},
		{"pr zero", [7]int64{10, 3, 4, 2000, 0, 6000, 200}},
		{"tmax zero", [7]int64{10, 3, 4, 2000, 800, 6000, 0}},
		{"tmax high", [7]int64{10, 3, 4, 2000, 800, 6000, 1_000_000_001}},
		{"num equals den", [7]int64{10, 4, 4, 2000, 800, 6000, 200}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := New(c.args[0], c.args[1], c.args[2], c.args[3], c.args[4], c.args[5], c.args[6]); !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("err=%v want ErrInvalidArgument", err)
			}
		})
	}
}

// At Ps exactly suppression triggers; one below does not.
func TestThresholdExact(t *testing.T) {
	// Pick decay 1/2 with delta 100 so nothing decays over the short test.
	d := newTestDamp(t, 100, 1, 2, 1000, 10, 2000, 1000)
	// withdraw=1000 exactly reaches Ps.
	d.Announce(1, 1, 1, 0)
	ev, err := d.Withdraw(1, 1, 5)
	if err != nil || len(ev) != 0 {
		t.Fatalf("withdraw exact Ps: ev=%v err=%v (basis: p==Ps suppresses)", ev, err)
	}
	if _, s, _ := d.Penalty(1, 1, 5); !s {
		t.Fatalf("p exactly Ps must suppress")
	}
	// Another route: 999 stays one below Ps.
	d2 := newTestDamp(t, 100, 1, 2, 1000, 10, 2000, 1000)
	d2.Announce(2, 2, 1, 0)
	d2.Withdraw(2, 2, 0) // p=1000 suppresses at t=0
	d2.Announce(2, 2, 1, 0)
	d2.Tick(1000) // release at Tmax=1000
	// after release p=0; announce attr-change adds 500 < Ps: no suppression
	ev, _ = d2.Announce(2, 2, 2, 1000)
	if len(ev) != 0 {
		t.Fatalf("unexpected events %v", ev)
	}
	if p, s, _ := d2.Penalty(2, 2, 1000); p != 500 || s {
		t.Fatalf("p=%d sup=%v want 500/false", p, s)
	}
}

// A decayed value exactly equal to Pr must NOT release; one less does.
func TestReuseExactPr(t *testing.T) {
	// num/den=1/2, Pr=500. Build p=2000 (2*1000): decay 1000,500,250 ->
	// equal to Pr at step 2 (not released), strictly below at step 3.
	d := newTestDamp(t, 10, 1, 2, 1500, 500, 6000, 1_000_000)
	d.Announce(1, 1, 1, 0)
	d.Withdraw(1, 1, 0) // p=1000, last=0
	d.Announce(1, 1, 1, 0)
	ev, _ := d.Withdraw(1, 1, 0) // p=2000, suppress, last=0
	if len(ev) != 0 {
		t.Fatalf("unexpected %v", ev)
	}
	// from last=0: t=10 ->1000, t=20 ->500 (==Pr, stay), t=30 ->250 (<Pr)
	if ev, _ := d.Tick(20); len(ev) != 0 {
		t.Fatalf("equal to Pr released: %v", ev)
	}
	if ev, _ := d.Tick(30); len(ev) != 1 || ev[0].ReuseAt != 30 {
		t.Fatalf("should release one below Pr at 30, got %v", ev)
	}
}

// last advances by whole steps only; setting it to now would shift reuseAt.
func TestPhaseAlignment(t *testing.T) {
	// Worked example numbers: reuseAt=65 vs erroneous 66.
	d := newTestDamp(t, 10, 3, 4, 2000, 800, 6000, 200)
	d.Announce(1, 1, 1, 0)
	d.Withdraw(1, 1, 5)
	d.Announce(1, 1, 1, 12)
	d.Withdraw(1, 1, 16)
	d.Announce(1, 1, 1, 24)
	d.Withdraw(1, 1, 26)    // suppress at 26, last=25, j=4 -> reuse 65
	d.Announce(1, 1, 1, 30) // advertised again while suppressed
	if ev, _ := d.Tick(64); len(ev) != 0 {
		t.Fatalf("reuse too early: %v", ev)
	}
	ev, _ := d.Tick(65)
	if len(ev) != 1 || ev[0].ReuseAt != 65 || !ev[0].Usable {
		t.Fatalf("phase: got %v want reuse at 65 usable (66 would be wrong)", ev)
	}
}

// Re-penalty while suppressed recomputes reuseAt but keeps supSince.
func TestRepenaltySuppressed(t *testing.T) {
	d := newTestDamp(t, 10, 3, 4, 2000, 800, 6000, 200)
	d.Announce(1, 1, 1, 0)
	d.Withdraw(1, 1, 5) // p=1000 last=5
	d.Announce(1, 1, 1, 12)
	d.Withdraw(1, 1, 16) // 1750
	d.Announce(1, 1, 1, 24)
	d.Withdraw(1, 1, 26)    // suppress; reuseAt 65, supSince=26
	d.Announce(1, 1, 1, 30) // back to advertised, no penalty
	// attr change at 40 adds 500 while suppressed; supSince stays 26.
	// settle: k=floor((40-25)/10)=1: p=1734 last=35; +500=2234.
	// sequence 1675,1256,942,706 -> j=4 -> reuseAt=75; Tmax=226.
	ev, err := d.Announce(1, 1, 2, 40)
	if err != nil || len(ev) != 0 {
		t.Fatalf("re-penalty announce: %v %v", ev, err)
	}
	if ev, _ := d.Tick(74); len(ev) != 0 {
		t.Fatalf("reuse before recomputed 75: %v", ev)
	}
	ev, _ = d.Tick(75)
	if len(ev) != 1 || ev[0].ReuseAt != 75 {
		t.Fatalf("recomputed reuseAt want 75, got %v", ev)
	}
	p, s, _ := d.Penalty(1, 1, 75)
	if s || p <= 0 {
		t.Fatalf("released route should read unsuppressed, p=%d", p)
	}
}

// Tmax release leaves p high; no new penalty means no re-suppression.
func TestTmaxReleaseKeepsPenalty(t *testing.T) {
	d := newTestDamp(t, 10, 3, 4, 2000, 800, 6000, 30)
	d.Announce(1, 1, 1, 0)
	d.Withdraw(1, 1, 5)
	d.Announce(1, 1, 1, 12)
	d.Withdraw(1, 1, 16)
	d.Announce(1, 1, 1, 24)
	d.Withdraw(1, 1, 26) // reuseAt=min(56,65)=56
	ev, _ := d.Tick(56)
	if len(ev) != 1 || ev[0].ReuseAt != 56 {
		t.Fatalf("tmax release: %v", ev)
	}
	// No settle on Tmax release: last=25, p=2312; Penalty settles to t=56:
	// k=3 steps ->975 (still > Pr), yet unsuppressed.
	p, s, _ := d.Penalty(1, 1, 56)
	if s || p != 975 {
		t.Fatalf("after Tmax p=%d sup=%v want 975/false", p, s)
	}
	// No penalty arriving, ticks never suppress again.
	if ev, _ := d.Tick(57); len(ev) != 0 {
		t.Fatalf("phantom event %v", ev)
	}
}

// A penalty exactly at reuseAt releases first, then re-evaluates.
func TestPenaltyAtReuseInstant(t *testing.T) {
	d := newTestDamp(t, 10, 3, 4, 2000, 800, 6000, 200)
	d.Announce(1, 1, 1, 0)
	d.Withdraw(1, 1, 5)
	d.Announce(1, 1, 1, 12)
	d.Withdraw(1, 1, 16)
	d.Announce(1, 1, 1, 24)
	d.Withdraw(1, 1, 26) // reuseAt 65
	d.Announce(1, 1, 2, 30)
	// Withdraw at exactly 65: release event (usable) then settle p 2312->731,
	// last=65, +1000=1731 < Ps -> not suppressed, route withdrawn.
	ev, err := d.Withdraw(1, 1, 65)
	if err != nil {
		t.Fatal(err)
	}
	if len(ev) != 1 || !ev[0].Usable || ev[0].ReuseAt != 65 {
		t.Fatalf("event at reuse instant: %v", ev)
	}
	p, s, _ := d.Penalty(1, 1, 65)
	if p != 1731 || s {
		t.Fatalf("p=%d sup=%v want 1731/false", p, s)
	}
	if _, err := d.Withdraw(1, 1, 66); !errors.Is(err, ErrRouteNotFound) {
		t.Fatalf("withdrawn route withdraw err=%v want ErrRouteNotFound", err)
	}
}

// PeerDown never adds penalties and preserves suppression; duplicate announces
// with equal attr change nothing.
func TestPeerDownAndDuplicate(t *testing.T) {
	d := newTestDamp(t, 10, 3, 4, 2000, 800, 6000, 200)
	d.Announce(1, 1, 1, 0)
	d.Announce(1, 1, 1, 1) // duplicate: no penalty
	if p, _, _ := d.Penalty(1, 1, 1); p != 0 {
		t.Fatalf("duplicate announce penalized: p=%d", p)
	}
	d.Announce(2, 1, 7, 1)
	d.Withdraw(2, 1, 5) // p=1000 for (2,1), last=5
	ev, err := d.PeerDown(1, 26)
	if err != nil || len(ev) != 0 {
		t.Fatalf("peerdown: %v %v", ev, err)
	}
	if p, _, _ := d.Penalty(1, 1, 26); p != 0 {
		t.Fatalf("PeerDown added penalty p=%d", p)
	}
	// peer 2 route untouched by PeerDown(1); Penalty settles to 26:
	// 1000 ->750 (t=15) ->562 (t=25), two steps from last=5.
	if p, _, _ := d.Penalty(2, 1, 26); p != 562 {
		t.Fatalf("PeerDown touched another peer: p=%d want 562", p)
	}
	if _, err := d.PeerDown(1, 27); !errors.Is(err, ErrPeerNotFound) {
		t.Fatalf("err=%v want ErrPeerNotFound", err)
	}
}

// Rejection precedence and rejection leaves no side effects.
func TestRejections(t *testing.T) {
	d := newTestDamp(t, 10, 3, 4, 2000, 800, 6000, 200)
	if _, err := d.Tick(-1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("tick neg: %v", err)
	}
	d.Tick(10)
	// invalid argument beats rollback
	if _, err := d.Tick(-1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("precedence: %v", err)
	}
	// rollback beats state error (route absent too): no state check first
	if _, err := d.Withdraw(9, 9, 5); !errors.Is(err, ErrClockRollback) {
		t.Fatalf("rollback precedence: %v", err)
	}
	// rejected operation does not release due routes or advance clock:
	d.Announce(3, 3, 1, 10)
	d.Withdraw(3, 3, 11) // p=1000
	d.Announce(3, 3, 1, 12)
	d.Withdraw(3, 3, 13) // settle 0 steps (last=11): p=1750
	d.Announce(3, 3, 1, 14)
	d.Withdraw(3, 3, 15) // suppress early-ish; compute reuse
	p, s, _ := d.Penalty(3, 3, 15)
	if !s {
		t.Fatalf("expected suppression, p=%d", p)
	}
	// rollback call at 12 must not drain or mutate
	if _, errRoll := d.Tick(12); !errors.Is(errRoll, ErrClockRollback) {
		t.Fatalf("err=%v", errRoll)
	}
	if ev, _ := d.Tick(15); len(ev) != 0 {
		t.Fatalf("rejected call released routes: %v", ev)
	}
	// unknown route withdraw -> route not found; unknown peer down likewise
	if _, err := d.Withdraw(7, 7, 16); !errors.Is(err, ErrRouteNotFound) {
		t.Fatalf("err=%v", err)
	}
}

// Invalid keys and attrs are rejected as arguments.
func TestInvalidKeys(t *testing.T) {
	d := newTestDamp(t, 10, 3, 4, 2000, 800, 6000, 200)
	if _, err := d.Announce(0, 1, 1, 0); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("peer 0: %v", err)
	}
	if _, err := d.Announce(1_000_001, 1, 1, 0); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("peer huge: %v", err)
	}
	if _, _, err := d.Penalty(1, 0, 0); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("prefix 0: %v", err)
	}
	if _, _, err := d.Penalty(1, 1, 1_000_000_000_001); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("now huge: %v", err)
	}
	// Missing record reads zero/un-suppressed.
	if p, s, err := d.Penalty(5, 5, 0); err != nil || p != 0 || s {
		t.Fatalf("missing record: p=%d s=%v err=%v", p, s, err)
	}
}

// Concurrency smoke: calls with identical timestamps run in parallel while
// rounds are barriered, so accepted timestamps never roll back.
func TestConcurrentSmoke(t *testing.T) {
	d := newTestDamp(t, 10, 3, 4, 2000, 800, 6000, 500)
	const peers = 8
	var ready, done sync.WaitGroup
	for n := int64(0); n < 50; n++ {
		now := n * 3
		ready.Add(peers)
		done.Add(peers)
		for peer := int64(1); peer <= peers; peer++ {
			go func(peer int64) {
				defer done.Done()
				d.Announce(peer, peer, n, now)
				ready.Done()
			}(peer)
		}
		ready.Wait()
		done.Wait()
		ready.Add(peers)
		done.Add(peers)
		for peer := int64(1); peer <= peers; peer++ {
			go func(peer int64) {
				defer done.Done()
				defer ready.Done()
				if n%2 == 0 {
					d.Withdraw(peer, peer, now+1)
				}
			}(peer)
		}
		ready.Wait()
		done.Wait()
	}
	for peer := int64(1); peer <= peers; peer++ {
		if p, _, err := d.Penalty(peer, peer, 149); err != nil || p < 0 || p > 6000 {
			t.Fatalf("peer %d p=%d err=%v", peer, p, err)
		}
	}
}
