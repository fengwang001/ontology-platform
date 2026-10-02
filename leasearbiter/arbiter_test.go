package leasearbiter

import (
	"errors"
	"testing"
)

func mustNew(t *testing.T, n int, dur int64, rho int, et, start int64) *Arbiter {
	t.Helper()
	a, err := New(n, dur, rho, et, start)
	if err != nil {
		t.Fatalf("New(%d,%d,%d,%d,%d): unexpected error %v", n, dur, rho, et, start, err)
	}
	return a
}

func challenger(t *testing.T, a *Arbiter, now int64) int {
	t.Helper()
	v, err := a.ChallengerVotes(now)
	if err != nil {
		t.Fatalf("ChallengerVotes(%d): %v", now, err)
	}
	return v
}

// TestSpecWalkthrough replays the worked example from the specification.
func TestSpecWalkthrough(t *testing.T) {
	// N=5 q=3, Dur=1000, Rho=100 (window 900), Et=1500, start=0.
	a := mustNew(t, 5, 1000, 100, 1500, 0)

	if conf, err := a.Ack(1, 100, 110); err != nil || len(conf) != 0 {
		t.Fatalf("Ack(1,100,110) = %v,%v", conf, err)
	}
	if conf, err := a.Ack(2, 105, 120); err != nil || len(conf) != 0 {
		t.Fatalf("Ack(2,105,120) = %v,%v", conf, err)
	}
	// base = 2nd largest follower ack = 100, E = 100+900 = 1000.
	if r, err := a.Read(999); err != nil || !r.Local || r.LeaseExpiry != 1000 {
		t.Fatalf("Read(999) = %+v,%v want local E=1000", r, err)
	}
	if r, err := a.Read(1000); err != nil || !r.Pending || r.ReadID != 1 {
		t.Fatalf("Read(1000) = %+v,%v want pending id 1", r, err)
	}

	if conf, err := a.Ack(3, 1001, 1010); err != nil || len(conf) != 0 {
		t.Fatalf("Ack(3,...): read 1 must remain pending, got %v,%v", conf, err)
	}
	if conf, err := a.Ack(4, 1002, 1020); err != nil || len(conf) != 1 || conf[0] != 1 {
		t.Fatalf("Ack(4,...) should confirm read 1, got %v,%v", conf, err)
	}

	// base=1001, E=1901; pr = 1110,1120,2010,2020.
	if got := challenger(t, a, 1900); got != 2 {
		t.Fatalf("ChallengerVotes(1900) = %d, want 2", got)
	}
	if r, err := a.Read(1900); err != nil || !r.Local || r.LeaseExpiry != 1901 {
		t.Fatalf("Read(1900) = %+v,%v want local E=1901", r, err)
	}
	if r, err := a.Read(1901); err != nil || !r.Pending || r.ReadID != 2 {
		t.Fatalf("Read(1901) = %+v,%v want pending id 2", r, err)
	}
	if got := challenger(t, a, 2025); got != 4 {
		t.Fatalf("ChallengerVotes(2025) = %d, want 4", got)
	}

	if aborted, err := a.Tick(2501); err != nil || aborted != nil {
		t.Fatalf("Tick(2501) = %v,%v want no demotion", aborted, err)
	}
	if aborted, err := a.Tick(2502); err != nil || len(aborted) != 1 || aborted[0] != 2 {
		t.Fatalf("Tick(2502) = %v,%v want aborted [2]", aborted, err)
	}
	if got := challenger(t, a, 2502); got != 5 {
		t.Fatalf("ChallengerVotes after demotion = %d, want 5 (leader votes too)", got)
	}
	if _, err := a.Read(2503); !errors.Is(err, ErrNotLeader) {
		t.Fatalf("Read after demotion err = %v, want ErrNotLeader", err)
	}
	// Ack still accepted after demotion.
	if _, err := a.Ack(1, 2600, 2600); err != nil {
		t.Fatalf("Ack after demotion: %v", err)
	}
}

// TestSingleNode verifies N=1: q=1, no followers, every read is local and
// the lease base is the read's own now.
func TestSingleNode(t *testing.T) {
	a := mustNew(t, 1, 1000, 100, 1500, 50)
	r, err := a.Read(50)
	if err != nil || !r.Local || r.LeaseExpiry != 950 {
		t.Fatalf("Read(50) = %+v,%v want local E=950", r, err)
	}
	// Lease follows each read's now, not stored acks.
	r, err = a.Read(10_000)
	if err != nil || !r.Local || r.LeaseExpiry != 10_900 {
		t.Fatalf("Read(10000) = %+v,%v want local E=10900", r, err)
	}
	if _, err := a.Ack(0, 0, 0); !errors.Is(err, ErrInvalidArgs) {
		t.Fatalf("Ack follower 0 on N=1 err=%v want ErrInvalidArgs", err)
	}
	// No followers: contact 0, but 0+1 >= q=1, so never demote.
	if aborted, err := a.Tick(1_000_000); err != nil || aborted != nil {
		t.Fatalf("single node must not demote: %v,%v", aborted, err)
	}
}

// TestSendTimeNotReceiveTime checks that lease base uses ack send time s,
// while the silence deadline pr uses receive time r + Dur.
func TestSendTimeNotReceiveTime(t *testing.T) {
	// N=3 q=2, need 1 follower ack. Dur=1000, Rho=0 -> window 1000.
	// Arbiter A: heartbeat sent at 100, received at 110. The lease is
	// anchored on the send timestamp, so now==E=1100 is already expired.
	a := mustNew(t, 3, 1000, 0, 2000, 0)
	if _, err := a.Ack(1, 100, 110); err != nil {
		t.Fatal(err)
	}
	if r, err := a.Read(1099); err != nil || !r.Local || r.LeaseExpiry != 1100 {
		t.Fatalf("Read(1099)=%+v,%v want local E=1100", r, err)
	}
	if r, err := a.Read(1100); err != nil || !r.Pending {
		t.Fatalf("Read(1100)=%+v,%v want pending (now==E is expired)", r, err)
	}
	// Arbiter B: same send time 100 but received very late at 1900. The
	// silence deadline uses the receive time: pr = 1900+1000 = 2900.
	b := mustNew(t, 3, 1000, 0, 2000, 0)
	if _, err := b.Ack(1, 100, 1900); err != nil {
		t.Fatal(err)
	}
	// Follower 1 is silence-bound until 2900; uncontacted follower 2 has
	// pr=0 and is already willing.
	if got := challenger(t, b, 2899); got != 1 {
		t.Fatalf("ChallengerVotes(2899)=%d want 1 (pr=2900 from receive time)", got)
	}
	if got := challenger(t, b, 2900); got != 2 {
		t.Fatalf("ChallengerVotes(2900)=%d want 2", got)
	}
}

// TestAckNeverRegresses verifies max semantics for both ack and pr.
func TestAckNeverRegresses(t *testing.T) {
	a := mustNew(t, 3, 1000, 0, 5000, 0)
	if _, err := a.Ack(1, 500, 600); err != nil {
		t.Fatal(err)
	}
	// Older send time at the same receive time: ack stays 500, pr = 1600.
	if _, err := a.Ack(1, 100, 600); err != nil {
		t.Fatal(err)
	}
	if a.ack[1] != 500 {
		t.Fatalf("ack regressed to %d, want 500", a.ack[1])
	}
	if a.pr[1] != 1600 {
		t.Fatalf("pr=%d want 1600", a.pr[1])
	}
	// Newer send advances ack; pr follows r+Dur monotonically.
	if _, err := a.Ack(1, 600, 700); err != nil {
		t.Fatal(err)
	}
	if a.ack[1] != 600 || a.pr[1] != 1700 {
		t.Fatalf("ack=%d pr=%d, want 600/1700", a.ack[1], a.pr[1])
	}
}

// TestDriftFloor checks floor(Dur*(1000-Rho)/1000).
func TestDriftFloor(t *testing.T) {
	cases := []struct {
		dur    int64
		rho    int
		window int64
	}{
		{1000, 100, 900},
		{1001, 1, 999}, // 1001*999/1000 = 999.999 -> 999
		{3, 999, 0},    // 3*1/1000 = 0
		{7, 333, 4},    // 7*667/1000 = 4.669 -> 4
		{1, 0, 1},
	}
	for _, c := range cases {
		a := mustNew(t, 1, c.dur, c.rho, c.dur, 0)
		if w := a.leaseWindow(); w != c.window {
			t.Fatalf("dur=%d rho=%d window=%d want %d", c.dur, c.rho, w, c.window)
		}
	}
}

// TestPendingConfirmOrder covers confirmation by send time >= arrival,
// stop-at-first-unconfirmed, and arrival-order dequeue.
func TestPendingConfirmOrder(t *testing.T) {
	// N=5 q=3 -> need 2 follower acks.
	// Dur=100 keeps the lease horizon tight; reads at now==E are pending.
	a := mustNew(t, 5, 100, 0, 10_000, 0)
	// First establish q-1 followers sending at 0; base=0, E=100.
	if _, err := a.Ack(1, 0, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Ack(2, 0, 0); err != nil {
		t.Fatal(err)
	}
	if r, err := a.Read(100); err != nil || !r.Pending || r.ReadID != 1 {
		t.Fatalf("Read(100)=%+v,%v want pending at now==E", r, err)
	}
	// An old send (100) from follower 3, received at 600, must not count
	// toward read1 (send time must be >= arrival 100). Inspection stops at
	// the first (and only) pending read.
	conf, err := a.Ack(3, 50, 100)
	if err != nil || len(conf) != 0 {
		t.Fatalf("old send must not confirm, got %v,%v", conf, err)
	}
	if a.Examined() != 1 {
		t.Fatalf("examined=%d want 1 (stopped at blocker)", a.Examined())
	}
	// Follower 1 resends at exactly 100: only one follower at >= 100 yet.
	conf, err = a.Ack(1, 100, 100)
	if err != nil || len(conf) != 0 {
		t.Fatalf("conf=%v,%v want none", conf, err)
	}
	// Follower 2 at exactly 100 confirms read1 (>= arrival inclusive).
	conf, err = a.Ack(2, 100, 100)
	if err != nil || len(conf) != 1 || conf[0] != 1 {
		t.Fatalf("conf=%v,%v want [1]", conf, err)
	}
	// Two more reads show arrival-order dequeue and the examined cap of
	// confirmed+1. Both arrive at 200 == the renewed E=200.
	if r, err := a.Read(200); err != nil || !r.Pending || r.ReadID != 2 {
		t.Fatalf("Read(200)=%+v,%v", r, err)
	}
	if r, err := a.Read(200); err != nil || !r.Pending || r.ReadID != 3 {
		t.Fatalf("Read(200)#2=%+v,%v", r, err)
	}
	// Only follower 1 reaches 200: both reads need two acks, stop at once.
	conf, err = a.Ack(1, 200, 200)
	if err != nil || len(conf) != 0 {
		t.Fatalf("conf=%v,%v want none", conf, err)
	}
	if a.Examined() != 1 {
		t.Fatalf("examined=%d want 1", a.Examined())
	}
	// Follower 2 at 200 supplies the second ack and both queued reads are
	// confirmed in arrival order; both were examined.
	conf, err = a.Ack(2, 200, 200)
	if err != nil || len(conf) != 2 || conf[0] != 2 || conf[1] != 3 {
		t.Fatalf("conf=%v,%v want [2 3]", conf, err)
	}
	if a.Examined() != 2 {
		t.Fatalf("examined=%d want 2", a.Examined())
	}
}

// TestDemotionBoundary covers start+Et grace, the inclusive contact
// boundary (ack == now-Et still counts) and abort of pending reads.
func TestDemotionBoundary(t *testing.T) {
	// N=3 q=2: demote iff contact+1 < 2, i.e. zero contacted followers.
	a := mustNew(t, 3, 1000, 0, 1500, 100)
	if _, err := a.Ack(1, 200, 200); err != nil {
		t.Fatal(err)
	}
	if r, err := a.Read(1200); err != nil || !r.Pending || r.ReadID != 1 {
		t.Fatalf("Read(1200)=%+v,%v want pending (lease E=1200 expired)", r, err)
	}
	// Before start+Et=1600 there is no demotion regardless of contact.
	if aborted, err := a.Tick(1599); err != nil || aborted != nil {
		t.Fatalf("grace period: %v,%v", aborted, err)
	}
	// At 1700 threshold = 1700-1500 = 200; ack 200 >= 200 still contacts.
	if aborted, err := a.Tick(1700); err != nil || aborted != nil {
		t.Fatalf("boundary ack==now-Et should count: %v,%v", aborted, err)
	}
	// At 1701 threshold 201: follower 1 stale -> contact 0 -> demote,
	// pending read aborted.
	aborted, err := a.Tick(1701)
	if err != nil || len(aborted) != 1 || aborted[0] != 1 {
		t.Fatalf("Tick(1701)=%v,%v want aborted [1]", aborted, err)
	}
	// A later Tick changes nothing and returns no (new) aborted ids.
	if aborted, err := a.Tick(1800); err != nil || aborted != nil {
		t.Fatalf("post-demotion tick: %v,%v", aborted, err)
	}
}

type stateSnap struct {
	ack, pr []int64
	pending []int
	leader  bool
	t       int64
}

func (a *Arbiter) snapshot() stateSnap {
	a.mu.Lock()
	defer a.mu.Unlock()
	s := stateSnap{leader: a.leader, t: a.t}
	s.ack = append([]int64(nil), a.ack...)
	s.pr = append([]int64(nil), a.pr...)
	for _, rd := range a.pending {
		s.pending = append(s.pending, rd.id)
	}
	return s
}

// TestRejectedOpsDoNotMutate verifies state is untouched by rejected calls
// and the exact error precedence: args, clock-skew, not-leader.
func TestRejectedOpsDoNotMutate(t *testing.T) {
	if _, err := New(0, 1000, 0, 1000, 0); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("N=0: %v", err)
	}
	if _, err := New(10, 1000, 0, 1000, 0); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("N=10: %v", err)
	}
	if _, err := New(3, 0, 0, 1000, 0); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("Dur=0: %v", err)
	}
	if _, err := New(3, 1000, 1000, 1000, 0); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("Rho=1000: %v", err)
	}
	if _, err := New(3, 1000, 0, 999, 0); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("Et<Dur: %v", err)
	}
	if _, err := New(3, 1_000_000_001, 0, 1_000_000_001, 0); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("Dur/Et overflow: %v", err)
	}
	if _, err := New(3, 1000, 0, 1000, -1); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("start=-1: %v", err)
	}

	a := mustNew(t, 3, 1000, 0, 5000, 10)
	if _, err := a.Ack(3, 0, 0); !errors.Is(err, ErrInvalidArgs) {
		t.Fatalf("Ack i out of range: %v", err)
	}
	if _, err := a.Ack(0, 0, 0); !errors.Is(err, ErrInvalidArgs) {
		t.Fatalf("Ack i=0: %v", err)
	}
	// s>r is an args error even when it would also look like clock skew.
	if _, err := a.Ack(1, 50, 40); !errors.Is(err, ErrInvalidArgs) {
		t.Fatalf("Ack s>r: %v", err)
	}
	if _, err := a.Ack(1, -1, 0); !errors.Is(err, ErrInvalidArgs) {
		t.Fatalf("Ack s<0: %v", err)
	}
	if _, err := a.Ack(1, 0, maxTime+1); !errors.Is(err, ErrInvalidArgs) {
		t.Fatalf("Ack r overflow: %v", err)
	}
	if _, err := a.Read(maxTime + 1); !errors.Is(err, ErrInvalidArgs) {
		t.Fatalf("Read overflow: %v", err)
	}
	if _, err := a.Tick(-1); !errors.Is(err, ErrInvalidArgs) {
		t.Fatalf("Tick -1: %v", err)
	}
	if _, err := a.ChallengerVotes(maxTime + 1); !errors.Is(err, ErrInvalidArgs) {
		t.Fatalf("Challenger overflow: %v", err)
	}

	// Establish state, then issue skew/leader errors; nothing may change.
	if _, err := a.Ack(1, 100, 100); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Read(100); err != nil {
		t.Fatal(err)
	}
	snap := a.snapshot()
	if _, err := a.Ack(1, 0, 99); !errors.Is(err, ErrClockSkew) {
		t.Fatalf("Ack skew: %v", err)
	}
	if _, err := a.Read(99); !errors.Is(err, ErrClockSkew) {
		t.Fatalf("Read skew: %v", err)
	}
	if _, err := a.Tick(99); !errors.Is(err, ErrClockSkew) {
		t.Fatalf("Tick skew: %v", err)
	}
	if _, err := a.ChallengerVotes(99); !errors.Is(err, ErrClockSkew) {
		t.Fatalf("Challenger skew: %v", err)
	}
	if got := a.snapshot(); !snapEqual(got, snap) {
		t.Fatalf("state changed after skew errors:\nbefore %+v\nafter  %+v", snap, got)
	}

	// Demote: Read must then report ErrNotLeader even with a valid clock.
	if _, err := a.Tick(5200); err != nil {
		t.Fatal(err)
	}
	snap = a.snapshot()
	if _, err := a.Read(5200); !errors.Is(err, ErrNotLeader) {
		t.Fatalf("Read after demotion: %v", err)
	}
	if got := a.snapshot(); !snapEqual(got, snap) {
		t.Fatalf("rejected Read after demotion mutated state")
	}
}

func snapEqual(x, y stateSnap) bool {
	if x.leader != y.leader || x.t != y.t || len(x.ack) != len(y.ack) ||
		len(x.pr) != len(y.pr) || len(x.pending) != len(y.pending) {
		return false
	}
	for i := range x.ack {
		if x.ack[i] != y.ack[i] || x.pr[i] != y.pr[i] {
			return false
		}
	}
	for i := range x.pending {
		if x.pending[i] != y.pending[i] {
			return false
		}
	}
	return true
}

// TestExaminedAtScale proves each Ack inspects at most confirmed+1 pending
// reads, and that the pattern is identical with 100 vs 10000 queued reads.
func TestExaminedAtScale(t *testing.T) {
	for _, size := range []int{100, 10000} {
		// N=5 q=3, need 2 follower acks. Reads arrive at successive times;
		// only follower 1 acks each arrival, so each Ack confirms nothing
		// and examines exactly 1 (stops at first unconfirmed).
		a := mustNew(t, 5, 1_000_000_000, 0, 1_000_000_000, 0)
		for k := 0; k < size; k++ {
			now := int64(k + 1)
			if r, err := a.Read(now); err != nil || !r.Pending || r.ReadID != k+1 {
				t.Fatalf("size=%d Read(%d)=%+v,%v", size, now, r, err)
			}
		}
		for k := 0; k < size; k++ {
			now := int64(size + k + 1)
			if conf, err := a.Ack(1, now, now); err != nil || len(conf) != 0 {
				t.Fatalf("size=%d Ack1 %d: %v,%v want none confirmed", size, k, conf, err)
			}
			if ex := a.Examined(); ex != 1 {
				t.Fatalf("size=%d step %d examined=%d want 1", size, k, ex)
			}
		}
		// One batch from follower 2 confirms all reads: a single Ack then
		// examines exactly `size`, i.e. confirmed+0 because no blocker.
		now := int64(3*size + 1)
		conf, err := a.Ack(2, now, now)
		if err != nil || len(conf) != size {
			t.Fatalf("size=%d final conf len=%d want %d (%v)", size, len(conf), size, err)
		}
		if ex := a.Examined(); ex != size {
			t.Fatalf("size=%d final examined=%d want %d", size, ex, size)
		}
	}
}
