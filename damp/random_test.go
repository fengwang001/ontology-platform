package damp

import (
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"
)

type rkey struct{ peer, prefix int64 }

// naiveRoute is per-route state of a millisecond-by-millisecond reference.
type naiveRoute struct {
	peer, prefix int64
	advertised   bool
	attr         int64
	has          bool
	p            int64
	last         int64
	suppressed   bool
	supSince     int64
}

type naiveModel struct {
	delta, num, den, ps, pr, pmax, tmax int64
	maxNow                              int64
	used                                bool
	routes                              map[rkey]*naiveRoute
}

func newNaive(delta, num, den, ps, pr, pmax, tmax int64) *naiveModel {
	return &naiveModel{delta: delta, num: num, den: den, ps: ps, pr: pr,
		pmax: pmax, tmax: tmax, routes: map[rkey]*naiveRoute{}}
}

func (n *naiveModel) step1(p int64) int64 { return p * n.num / n.den }

// advance moves one millisecond at a time; at each phase boundary one floor
// step is applied, and due routes are released. Events are sorted by
// (reuseAt, peer, prefix), matching the heap scheduler.
func (n *naiveModel) advance(to int64) []Event {
	var evs []Event
	for t := n.maxNow + 1; t <= to; t++ {
		var atT []Event
		for _, r := range n.routes {
			if !r.suppressed || !r.has {
				continue
			}
			for r.last+n.delta <= t {
				r.last += n.delta
				r.p = n.step1(r.p)
			}
			if r.p < n.pr || t >= r.supSince+n.tmax {
				r.suppressed = false
				atT = append(atT, Event{Peer: r.peer, Prefix: r.prefix, ReuseAt: t, Usable: r.advertised})
			}
		}
		sort.Slice(atT, func(i, j int) bool {
			if atT[i].ReuseAt != atT[j].ReuseAt {
				return atT[i].ReuseAt < atT[j].ReuseAt
			}
			if atT[i].Peer != atT[j].Peer {
				return atT[i].Peer < atT[j].Peer
			}
			return atT[i].Prefix < atT[j].Prefix
		})
		evs = append(evs, atT...)
	}
	n.maxNow = to
	n.used = true
	return evs
}

func (n *naiveModel) get(peer, prefix int64) *naiveRoute {
	k := rkey{peer, prefix}
	r := n.routes[k]
	if r == nil {
		r = &naiveRoute{peer: peer, prefix: prefix}
		n.routes[k] = r
	}
	return r
}

// settle applies floor steps from last up to t, advancing last by k*delta.
func (n *naiveModel) settle(r *naiveRoute, t int64) {
	if !r.has {
		return
	}
	k := (t - r.last) / n.delta
	for i := int64(0); i < k; i++ {
		r.p = n.step1(r.p)
	}
	r.last += k * n.delta
}

func (n *naiveModel) reschedule(r *naiveRoute) {
	// Derive reuse time the same way but kept implicit in advance();
	// nothing to store: suppression release is checked per millisecond.
}

func (n *naiveModel) penalize(r *naiveRoute, amount, now int64) {
	if !r.has {
		r.has = true
		r.p = 0
		r.last = now
	}
	n.settle(r, now)
	r.p += amount
	if r.p > n.pmax {
		r.p = n.pmax
	}
	if !r.suppressed && r.p >= n.ps {
		r.suppressed = true
		r.supSince = now
	}
}

func validKeyN(peer, prefix int64) bool {
	return 1 <= peer && peer <= 1_000_000 && 1 <= prefix && prefix <= 1_000_000_000
}
func validNowN(now int64) bool { return 0 <= now && now <= 1_000_000_000_000 }

func (n *naiveModel) rollback(now int64) bool { return n.used && now < n.maxNow }

func (n *naiveModel) announce(peer, prefix, attr, now int64) ([]Event, string) {
	if !validKeyN(peer, prefix) || !validNowN(now) {
		return nil, "invalid"
	}
	if n.rollback(now) {
		return nil, "rollback"
	}
	evs := n.advance(now)
	r := n.get(peer, prefix)
	if !r.advertised {
		r.advertised = true
		r.attr = attr
	} else if r.attr != attr {
		r.attr = attr
		n.penalize(r, 500, now)
	}
	return evs, "ok"
}

func (n *naiveModel) withdraw(peer, prefix, now int64) ([]Event, string) {
	if !validKeyN(peer, prefix) || !validNowN(now) {
		return nil, "invalid"
	}
	if n.rollback(now) {
		return nil, "rollback"
	}
	r := n.routes[rkey{peer, prefix}]
	if r == nil || !r.advertised {
		return nil, "route"
	}
	evs := n.advance(now)
	r.advertised = false
	n.penalize(r, 1000, now)
	return evs, "ok"
}

func (n *naiveModel) peerDown(peer, now int64) ([]Event, string) {
	if peer < 1 || peer > 1_000_000 || !validNowN(now) {
		return nil, "invalid"
	}
	if n.rollback(now) {
		return nil, "rollback"
	}
	exists := false
	for _, r := range n.routes {
		if r.peer == peer && r.advertised {
			exists = true
			break
		}
	}
	if !exists {
		return nil, "peer"
	}
	evs := n.advance(now)
	for _, r := range n.routes {
		if r.peer == peer && r.advertised {
			r.advertised = false
		}
	}
	return evs, "ok"
}

func (n *naiveModel) tick(now int64) ([]Event, string) {
	if !validNowN(now) {
		return nil, "invalid"
	}
	if n.rollback(now) {
		return nil, "rollback"
	}
	return n.advance(now), "ok"
}

// penalty is read-only: settle a copy and factor in due suppression release.
func (n *naiveModel) penalty(peer, prefix, t int64) (int64, bool, string) {
	if !validKeyN(peer, prefix) || !validNowN(t) {
		return 0, false, "invalid"
	}
	if n.used && t < n.maxNow {
		return 0, false, "rollback"
	}
	r := n.routes[rkey{peer, prefix}]
	if r == nil || !r.has {
		return 0, false, "ok"
	}
	cp := *r
	n.settle(&cp, t)
	sup := cp.suppressed
	if sup {
		// simulate release up to t without mutating shared state
		for tt := n.maxNow + 1; tt <= t; tt++ {
			for cp.last+n.delta <= tt {
				cp.last += n.delta
				cp.p = n.step1(cp.p)
			}
			if cp.p < n.pr || tt >= cp.supSince+n.tmax {
				sup = false
				break
			}
		}
	}
	return cp.p, sup, "ok"
}

var _ = fmt.Sprintf

type genOp struct {
	kind               string
	peer, prefix, attr int64
	now                int64
}

func classifyErr(err error) string {
	switch {
	case err == nil:
		return "ok"
	case isInvalid(err, ErrInvalidArgument):
		return "invalid"
	case isInvalid(err, ErrClockRollback):
		return "rollback"
	case isInvalid(err, ErrRouteNotFound):
		return "route"
	case isInvalid(err, ErrPeerNotFound):
		return "peer"
	default:
		return "other"
	}
}

func isInvalid(err, target error) bool {
	return err != nil && (err == target || err.Error() == target.Error())
}

// TestRandomAgainstNaive replays 1500 random operation sequences through both
// the implementation and a per-millisecond reference, comparing events,
// rejection classes, penalties and suppression state after every accepted op.
func TestRandomAgainstNaive(t *testing.T) {
	const sequences = 1500
	cfgs := [][7]int64{
		{10, 3, 4, 2000, 800, 6000, 200},
		{7, 1, 2, 1500, 200, 4000, 120},
		{1, 9, 10, 100, 10, 2000, 60},
		{13, 2, 3, 800, 300, 5000, 90},
		{5, 1, 3, 500, 50, 3000, 70},
		{20, 4, 5, 1200, 600, 4000, 300},
	}
	for seq := 0; seq < sequences; seq++ {
		rng := rand.New(rand.NewSource(int64(seq)*1000003 + 7))
		cfg := cfgs[seq%len(cfgs)]
		d, err := New(cfg[0], cfg[1], cfg[2], cfg[3], cfg[4], cfg[5], cfg[6])
		if err != nil {
			t.Fatalf("seq %d New: %v", seq, err)
		}
		m := newNaive(cfg[0], cfg[1], cfg[2], cfg[3], cfg[4], cfg[5], cfg[6])

		var log []string
		horizon := int64(90)
		nops := 18 + rng.Intn(16)
		var cursor int64
		for i := 0; i < nops; i++ {
			kind := []string{"announce", "withdraw", "peerdown", "tick", "penalty"}[rng.Intn(5)]
			now := cursor + int64(rng.Intn(6))
			if rng.Intn(7) == 0 {
				now = cursor - int64(1+rng.Intn(3)) // deliberate rollback
			}
			if now < 0 {
				now = 0
			}
			peer := int64(1 + rng.Intn(2))
			prefix := int64(1 + rng.Intn(3))
			attr := int64(rng.Intn(3)) // churn attributes a bit
			_ = genOp{kind: kind, peer: peer, prefix: prefix, attr: attr, now: now}
			log = append(log, fmt.Sprintf("op%d %s peer=%d prefix=%d attr=%d now=%d",
				i, kind, peer, prefix, attr, now))

			var gotEvs []Event
			var gotCls string
			var gotP int64
			var gotSup bool
			switch kind {
			case "announce":
				gotEvs, err = d.Announce(peer, prefix, attr, now)
			case "withdraw":
				gotEvs, err = d.Withdraw(peer, prefix, now)
			case "peerdown":
				gotEvs, err = d.PeerDown(peer, now)
			case "tick":
				gotEvs, err = d.Tick(now)
			case "penalty":
				gotP, gotSup, err = d.Penalty(peer, prefix, now)
			}
			gotCls = classifyErr(err)

			var wantEvs []Event
			var wantCls string
			var wantP int64
			var wantSup bool
			switch kind {
			case "announce":
				wantEvs, wantCls = m.announce(peer, prefix, attr, now)
			case "withdraw":
				wantEvs, wantCls = m.withdraw(peer, prefix, now)
			case "peerdown":
				wantEvs, wantCls = m.peerDown(peer, now)
			case "tick":
				wantEvs, wantCls = m.tick(now)
			case "penalty":
				wantP, wantSup, wantCls = m.penalty(peer, prefix, now)
			}
			log = append(log, fmt.Sprintf("  => got cls=%s evs=%v %s ; want cls=%s evs=%v",
				gotCls, gotEvs, pstr(kind, gotP, gotSup), wantCls, wantEvs))

			if gotCls != wantCls {
				t.Fatalf("seq %d %s: rejection class got %s want %s\n%s",
					seq, kind, gotCls, wantCls, strings.Join(log, "\n"))
			}
			if !eventsEqual(gotEvs, wantEvs) {
				t.Fatalf("seq %d %s: events differ\n got %v\nwant %v\n%s",
					seq, kind, gotEvs, wantEvs, strings.Join(log, "\n"))
			}
			if kind == "penalty" && gotCls == "ok" {
				if gotP != wantP || gotSup != wantSup {
					t.Fatalf("seq %d penalty got (%d,%v) want (%d,%v)\n%s",
						seq, gotP, gotSup, wantP, wantSup, strings.Join(log, "\n"))
				}
			}
			if gotCls == "ok" && now > cursor {
				cursor = now
			}
			// invariants after each accepted operation
			if gotCls == "ok" {
				for k := range m.routes {
					p, sup, perr := d.Penalty(k.peer, k.prefix, cursor)
					if perr != nil {
						t.Fatalf("seq %d invariant penalty err: %v", seq, perr)
					}
					wp, wsup, _ := m.penalty(k.peer, k.prefix, cursor)
					if p != wp || sup != wsup {
						t.Fatalf("seq %d route (%d,%d) at %d: got (%d,%v) want (%d,%v)\n%s",
							seq, k.peer, k.prefix, cursor, p, sup, wp, wsup, strings.Join(log, "\n"))
					}
					if p < 0 || p > cfg[5] {
						t.Fatalf("seq %d p out of bounds: %d", seq, p)
					}
				}
			}
		}
		// advance far beyond any suppression to confirm final quiescence matches
		ge, gerr := d.Tick(horizon + 2000)
		we, _ := m.tick(horizon + 2000)
		if gerr != nil || !eventsEqual(ge, we) {
			t.Fatalf("seq %d tail events got %v (%v) want %v\n%s", seq, ge, gerr, we, strings.Join(log, "\n"))
		}
		// Verbose evidence: print one sequence's input/output/rationale.
		if seq == 0 {
			t.Logf("sample sequence %d cfg=%v\n%s", seq, cfg, strings.Join(log, "\n"))
		}
	}
}

func pstr(kind string, p int64, sup bool) string {
	if kind != "penalty" {
		return ""
	}
	return fmt.Sprintf("p=%d sup=%v", p, sup)
}

func eventsEqual(a, b []Event) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestMulStepsBound proves each settle/reuse computation is independent of the
// elapsed step count: a 10^3-step idle gap and a 10^9-step idle gap both cost
// at most Z+1 multiplications.
func TestMulStepsBound(t *testing.T) {
	d := newTestDamp(t, 10, 3, 4, 2000, 800, 6000, 200)
	z := d.Z()
	// Build a suppressed route with a record so arithmetic is exercised.
	d.Announce(1, 1, 1, 0)
	d.Withdraw(1, 1, 1) // settle from birth at t=1
	if got := d.MulSteps(); got > z+1 {
		t.Fatalf("short settle: %d > Z+1 %d", got, z+1)
	}
	// Read-only settle across a 10^3-step gap.
	if _, _, err := d.Penalty(1, 1, 1+10_000); err != nil {
		t.Fatal(err)
	}
	if got := d.MulSteps(); got > z+1 {
		t.Fatalf("10^3 idle: %d > Z+1 %d", got, z+1)
	}
	// A second route, penalty across a 10^9-step gap.
	d2 := newTestDamp(t, 10, 3, 4, 2000, 800, 6000, 200)
	d2.Announce(2, 2, 1, 0)
	d2.Withdraw(2, 2, 0)
	if _, _, err := d2.Penalty(2, 2, 10_000_000_000); err != nil {
		t.Fatal(err)
	}
	if got := d2.MulSteps(); got > z+1 {
		t.Fatalf("10^9 idle: %d > Z+1 %d", got, z+1)
	}
	// A re-penalty after a 10^9-step gap exercises both bounded settle and
	// StepsToBelow on the freshly clamped penalty.
	d3 := newTestDamp(t, 10, 3, 4, 2000, 800, 6000, 1_000_000_000)
	d3.Announce(3, 3, 1, 0)
	if _, err := d3.Withdraw(3, 3, 10_000_000_000); err != nil {
		t.Fatal(err)
	} // settle from 0 to 1e10 (p decays to zero), then +1000
	if _, err := d3.Announce(3, 3, 2, 10_000_000_000); err != nil {
		t.Fatal(err)
	} // advertised again, attr recorded, no penalty
	if _, err := d3.Announce(3, 3, 3, 10_000_000_000); err != nil {
		t.Fatal(err)
	} // attr change: +500 -> 1500, no suppress
	if _, err := d3.Withdraw(3, 3, 10_000_000_000); err != nil {
		t.Fatal(err)
	} // +1000 -> 2500 suppress: StepsToBelow just executed
	if got := d3.MulSteps(); got > z+1 {
		t.Fatalf("StepsToBelow after 10^9 idle: %d > Z+1 %d", got, z+1)
	}
}
