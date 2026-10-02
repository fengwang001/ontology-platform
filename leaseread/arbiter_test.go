package leaseread

import (
	"errors"
	"testing"
)

// TestExampleFromSpec replays the worked example in the task statement.
func TestExampleFromSpec(t *testing.T) {
	a, err := New(5, 1000, 100, 1500, 0)
	if err != nil {
		t.Fatal(err)
	}
	mustAck := func(i int, s, r int64) []int64 {
		t.Helper()
		ids, err := a.Ack(i, s, r)
		if err != nil {
			t.Fatalf("Ack(%d,%d,%d): %v", i, s, r, err)
		}
		return ids
	}
	mustRead := func(now int64) ReadResult {
		t.Helper()
		res, err := a.Read(now)
		if err != nil {
			t.Fatalf("Read(%d): %v", now, err)
		}
		return res
	}

	mustAck(1, 100, 110)
	mustAck(2, 105, 120)
	// base = 100, E = 1000.
	if r := mustRead(999); r.Kind != "local" || r.Expire != 1000 {
		t.Fatalf("Read(999) = %+v", r)
	}
	if r := mustRead(1000); r.Kind != "pending" || r.Id != 1 {
		t.Fatalf("Read(1000) = %+v, want pending id 1", r)
	}

	if ids := mustAck(3, 1001, 1010); len(ids) != 0 {
		t.Fatalf("Ack(3,...) confirmed %v, want none", ids)
	}
	if ids := mustAck(4, 1002, 1020); len(ids) != 1 || ids[0] != 1 {
		t.Fatalf("Ack(4,...) confirmed %v, want [1]", ids)
	}
	// base = 1001, E = 1901.
	if r := mustRead(1900); r.Kind != "local" || r.Expire != 1901 {
		t.Fatalf("Read(1900) = %+v", r)
	}
	if v, err := a.ChallengerVotes(1900); err != nil || v != 2 {
		t.Fatalf("ChallengerVotes(1900) = %d,%v, want 2", v, err)
	}
	if r := mustRead(1901); r.Kind != "pending" || r.Id != 2 {
		t.Fatalf("Read(1901) = %+v, want pending id 2", r)
	}
	if v, err := a.ChallengerVotes(2025); err != nil || v != 4 {
		t.Fatalf("ChallengerVotes(2025) = %d,%v, want 4", v, err)
	}

	if ids, err := a.Tick(2501); err != nil || len(ids) != 0 || !a.leader {
		t.Fatalf("Tick(2501) = %v,%v leader=%v", ids, err, a.leader)
	}
	ids, err := a.Tick(2502)
	if err != nil || len(ids) != 1 || ids[0] != 2 {
		t.Fatalf("Tick(2502) = %v,%v, want aborted [2]", ids, err)
	}
	if a.leader {
		t.Fatal("should have stepped down at 2502")
	}
}

func TestConfigRejected(t *testing.T) {
	bad := []struct {
		n                int
		dur, rho, et, st int64
	}{
		{0, 10, 0, 10, 0},
		{10, 10, 0, 10, 0},
		{3, 0, 0, 10, 0},
		{3, 1_000_000_001, 0, 1_000_000_001, 0},
		{3, 10, -1, 10, 0},
		{3, 10, 1000, 10, 0},
		{3, 10, 0, 9, 0}, // Et < Dur
		{3, 10, 0, 10, -1},
		{3, 10, 0, 10, maxTime + 1},
	}
	for _, c := range bad {
		if _, err := New(c.n, c.dur, c.rho, c.et, c.st); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("New(%+v) err = %v, want ErrInvalidConfig", c, err)
		}
	}
	if _, err := New(9, 1, 0, 1, maxTime); err != nil {
		t.Fatalf("boundary valid config rejected: %v", err)
	}
}

func TestN1AlwaysLocal(t *testing.T) {
	a, _ := New(1, 1000, 250, 1000, 50)
	if r, err := a.Read(60); err != nil || r.Kind != "local" || r.Expire != 60+750 {
		t.Fatalf("Read = %+v,%v", r, err)
	}
	if r, _ := a.Read(10000); r.Kind != "local" || r.Expire != 10750 {
		t.Fatalf("Read(10000) = %+v", r)
	}
	if v, err := a.ChallengerVotes(10000); err != nil || v != 0 {
		t.Fatalf("votes = %d,%v", v, err)
	}
	// c+1 = 1 >= q = 1: never steps down.
	if ids, err := a.Tick(1_000_000); err != nil || len(ids) != 0 || !a.leader {
		t.Fatalf("Tick = %v,%v leader=%v", ids, err, a.leader)
	}
}

func TestSendTimeNotReceiveAndMaxAck(t *testing.T) {
	// Dur=1000, Rho=0 => lease length 1000.
	a, _ := New(3, 1000, 0, 1000, 0)
	if _, err := a.Ack(1, 100, 900); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Ack(2, 200, 900); err != nil {
		t.Fatal(err)
	}
	// N=3 => q-1=1, so base = max send = 200; E = 1200 regardless of
	// receive time 900: send timestamps drive the lease.
	if r, _ := a.Read(1100); r.Kind != "local" || r.Expire != 1200 {
		t.Fatalf("Read(1100) = %+v, want local E=1200", r)
	}
	if r, _ := a.Read(1200); r.Kind != "pending" || r.Id != 1 {
		t.Fatalf("Read(1200) = %+v, now==E must pend", r)
	}
	// Read 1 (a=1200): ack1=100, ack2=200, neither >= 1200. A fresh send
	// from any follower confirms (need q-1=1).
	// Older send with a later receive still updates pr but not ack.
	if _, err := a.Ack(1, 50, 1200); err != nil {
		t.Fatal(err)
	}
	if ids, err := a.Ack(2, 1200, 1201); err != nil || len(ids) != 1 || ids[0] != 1 {
		t.Fatalf("confirm = %v,%v", ids, err)
	}
}

func TestFloorLeaseLength(t *testing.T) {
	// Dur=3, Rho=1: floor(3*999/1000) = 2.
	a, _ := New(3, 3, 1, 3, 0)
	a.Ack(1, 10, 10)
	a.Ack(2, 10, 10)
	if r, _ := a.Read(11); r.Kind != "local" {
		t.Fatalf("Read(11) = %+v, want local until 12", r)
	}
	if r, _ := a.Read(12); r.Kind != "pending" {
		t.Fatalf("Read(12) = %+v, now==E must not be local", r)
	}
}

func TestPendingOnlyConfirmedByLaterSends(t *testing.T) {
	a, _ := New(5, 1000, 0, 1500, 0)
	// Two sends at time 50: N=5 q-1=2, base = 50, E = 1050.
	a.Ack(1, 50, 50)
	a.Ack(2, 50, 50)
	if r, _ := a.Read(1049); r.Kind != "local" || r.Expire != 1050 {
		t.Fatalf("Read(1049) = %+v", r)
	}
	// Read at 1050 (== E) pends; the old sends at 50 must not confirm it.
	if r, _ := a.Read(1050); r.Kind != "pending" || r.Id != 1 {
		t.Fatalf("Read(1050) = %+v", r)
	}
	// One fresh send >= 1050 is not enough (need 2).
	if _, err := a.Ack(1, 1050, 1050); err != nil {
		t.Fatal(err)
	}
	if len(a.pending) != 1 {
		t.Fatal("read 1 must still be pending")
	}
	// Second fresh send confirms it.
	ids, _ := a.Ack(2, 1050, 1050)
	if len(ids) != 1 || ids[0] != 1 {
		t.Fatalf("confirm = %v", ids)
	}
}

func TestStopAtFirstUnconfirmed(t *testing.T) {
	a, _ := New(3, 1000, 0, 100000, 0) // need one follower send >= a
	a.Ack(1, 1, 1)
	a.Ack(2, 1, 1)
	// Lease E = 1+1000 = 1001, so reads from 1001 queue despite old acks.
	if r, _ := a.Read(1001); r.Kind != "pending" || r.Id != 1 {
		t.Fatal(r)
	}
	if r, _ := a.Read(1100); r.Kind != "pending" || r.Id != 2 {
		t.Fatal(r)
	}
	if r, _ := a.Read(1200); r.Kind != "pending" || r.Id != 3 {
		t.Fatal(r)
	}
	// Arrival order is 1(1001), 2(1100), 3(1200): send 1150 confirms 1
	// and 2, then 3 blocks.
	ids, _ := a.Ack(1, 1150, 1201)
	if len(ids) != 2 || ids[0] != 1 || ids[1] != 2 {
		t.Fatalf("confirmed %v, want [1 2]", ids)
	}
	if a.examined != 3 {
		t.Fatalf("examined = %d, want 3 (2 confirmed + 1 blocked)", a.examined)
	}
	// Send 1200 confirms the last pending read.
	ids, _ = a.Ack(1, 1200, 1202)
	if len(ids) != 1 || ids[0] != 3 {
		t.Fatalf("confirmed %v, want [3]", ids)
	}
	if a.examined != 1 {
		t.Fatalf("examined = %d, want 1", a.examined)
	}
}

func TestPrMaxAndSilence(t *testing.T) {
	a, _ := New(3, 1000, 0, 5000, 0)
	a.Ack(1, 100, 100) // pr1 = 1100
	a.Ack(1, 50, 200)  // older send, later receive: pr1 = max(1100,1200)
	a.Ack(2, 100, 100) // pr2 = 1100
	if v, _ := a.ChallengerVotes(1199); v != 1 {
		t.Fatalf("votes at 1199 = %d, want 1 (follower 2 only)", v)
	}
	if v, _ := a.ChallengerVotes(1200); v != 2 {
		t.Fatalf("votes at 1200 = %d, want 2", v)
	}
}

func TestStepdownBoundaryAndAbort(t *testing.T) {
	a, _ := New(5, 1000, 0, 1500, 10)
	// At now=1510 the contact threshold is 10; sends exactly at 10 count.
	a.Ack(3, 10, 10)
	a.Ack(4, 10, 10)
	// Before start+Et=1510: no step-down even with poor contact.
	if ids, _ := a.Tick(1509); len(ids) != 0 || !a.leader {
		t.Fatalf("Tick(1509) = %v leader=%v", ids, a.leader)
	}
	// At 1510: c=2, c+1=3 >= q: stays leader. Then old sends with later
	// receives raise pr without adding fresh contact.
	if ids, _ := a.Tick(1510); len(ids) != 0 || !a.leader {
		t.Fatalf("Tick(1510) = %v leader=%v", ids, a.leader)
	}
	a.Ack(3, 10, 1510)
	a.Ack(4, 10, 1510)
	a.Ack(1, 0, 1510)
	a.Ack(2, 0, 1510)
	// At 1511 threshold=11: every ack send is < 11 -> c=0, step down.
	if r, _ := a.Read(1511); r.Kind != "pending" || r.Id != 1 {
		t.Fatal(r)
	}
	ids, _ := a.Tick(1511)
	if len(ids) != 1 || ids[0] != 1 || a.leader {
		t.Fatalf("Tick(1511) = %v leader=%v", ids, a.leader)
	}
	// After step-down Read is rejected; Ack still accepted; leader votes too.
	if _, err := a.Read(1512); !errors.Is(err, ErrNotLeader) {
		t.Fatalf("Read after stepdown err = %v", err)
	}
	if v, _ := a.ChallengerVotes(1512); v != 1 {
		t.Fatalf("votes = %d, want only the deposed leader", v)
	}
	// A post-stepdown Ack is still accepted (pr1 = 1612+1000 = 2612).
	if ids, err := a.Ack(1, 1512, 1612); err != nil || len(ids) != 0 {
		t.Fatalf("Ack after stepdown = %v,%v", ids, err)
	}
	if _, err := a.Read(1613); !errors.Is(err, ErrNotLeader) {
		t.Fatalf("Read after post-stepdown ack err = %v", err)
	}
	if v, _ := a.ChallengerVotes(2612); v != 5 {
		t.Fatalf("votes at 2612 = %d, want all 5 once silence ends", v)
	}
}

func TestRejectedOpsDoNotMutate(t *testing.T) {
	a, _ := New(3, 1000, 0, 1500, 0)
	a.Ack(1, 100, 100)

	// Ack: invalid arg beats clock rewind.
	if _, err := a.Ack(2, 200, 100); !errors.Is(err, ErrInvalidArg) {
		t.Fatalf("s>r: %v", err)
	}
	if _, err := a.Ack(0, 100, 100); !errors.Is(err, ErrInvalidArg) {
		t.Fatalf("i out of range: %v", err)
	}
	if _, err := a.Ack(1, -1, 100); !errors.Is(err, ErrInvalidArg) {
		t.Fatalf("s<0: %v", err)
	}
	if _, err := a.Ack(1, 99, 99); !errors.Is(err, ErrClockRewind) {
		t.Fatalf("rewind: %v", err)
	}
	// Read: invalid arg, then rewind, then not-leader.
	if _, err := a.Read(maxTime + 1); !errors.Is(err, ErrInvalidArg) {
		t.Fatalf("now out of range: %v", err)
	}
	if _, err := a.Read(99); !errors.Is(err, ErrClockRewind) {
		t.Fatalf("read rewind: %v", err)
	}
	// Tick / ChallengerVotes: invalid arg, then rewind.
	if _, err := a.Tick(-1); !errors.Is(err, ErrInvalidArg) {
		t.Fatalf("tick invalid: %v", err)
	}
	if _, err := a.Tick(99); !errors.Is(err, ErrClockRewind) {
		t.Fatalf("tick rewind: %v", err)
	}
	if _, err := a.ChallengerVotes(99); !errors.Is(err, ErrClockRewind) {
		t.Fatalf("votes rewind: %v", err)
	}

	// Trigger a legal step-down and then repeat rejections; state must stay.
	a.Tick(1000)
	a.Tick(1600) // threshold 100: ack1=100 counts -> c=1, stays leader
	if !a.leader {
		t.Fatal("unexpected stepdown")
	}
	snap := a.snapshot()
	if _, err := a.Read(1599); !errors.Is(err, ErrClockRewind) {
		t.Fatalf("post-tick rewind: %v", err)
	}
	if _, err := a.Ack(1, 1500, 1599); !errors.Is(err, ErrClockRewind) {
		t.Fatalf("post-tick ack rewind: %v", err)
	}
	if _, err := a.Tick(1599); !errors.Is(err, ErrClockRewind) {
		t.Fatalf("post-tick tick rewind: %v", err)
	}
	if got := a.snapshot(); !statesEqual(got, snap) {
		t.Fatalf("state changed by rejected op:\nbefore %+v\nafter  %+v", snap, got)
	}
}

type arbiterState struct {
	ack     []int64
	pr      []int64
	pending []pendingRead
	leader  bool
	t       int64
	nextId  int64
}

func statesEqual(x, y arbiterState) bool {
	pendingEqual := len(x.pending) == len(y.pending)
	if pendingEqual {
		for i := range x.pending {
			if x.pending[i] != y.pending[i] {
				pendingEqual = false
				break
			}
		}
	}
	return slicesEqual(x.ack, y.ack) &&
		slicesEqual(x.pr, y.pr) &&
		pendingEqual &&
		x.leader == y.leader && x.t == y.t && x.nextId == y.nextId
}

func slicesEqual(x, y []int64) bool {
	if len(x) != len(y) {
		return false
	}
	for i := range x {
		if x[i] != y[i] {
			return false
		}
	}
	return true
}

func (a *Arbiter) snapshot() arbiterState {
	return arbiterState{
		ack:     append([]int64(nil), a.ack...),
		pr:      append([]int64(nil), a.pr...),
		pending: append([]pendingRead(nil), a.pending...),
		leader:  a.leader,
		t:       a.t,
		nextId:  a.nextId,
	}
}

// TestExaminedSameAt100And10000 verifies the examined bound and that the
// number of examined reads is identical with 100 and 10000 pending reads
// when the prefix behaves the same.
func TestExaminedSameAt100And10000(t *testing.T) {
	run := func(queued int) (int64, []int64) {
		a, _ := New(3, 1, 0, 1_000_000_000, 0)
		for k := 0; k < queued; k++ {
			r, _ := a.Read(int64(10 + k))
			if r.Kind != "pending" {
				t.Fatalf("seed read %d = %+v", k, r)
			}
		}
		// Send covers only the first 40 arrival times, then one that
		// covers the rest: examination stops at the first gap.
		ids, err := a.Ack(1, int64(10+39), int64(1000+queued))
		if err != nil {
			t.Fatal(err)
		}
		return a.examined, ids
	}
	e100, ids100 := run(100)
	e10k, ids10k := run(10000)
	if e100 != 41 || e10k != 41 {
		t.Fatalf("examined 100=%d 10000=%d, want 41", e100, e10k)
	}
	if len(ids100) != 40 || len(ids10k) != 40 {
		t.Fatalf("confirmed 100=%d 10000=%d, want 40", len(ids100), len(ids10k))
	}
}
