package jit

import (
	"sync"
	"sync/atomic"
	"testing"
)

func baseConfig() Config {
	return Config{
		N: 8, A1: 5, M1: 3, B1: 12, A2: 10, M2: 6, B2: 30,
		F: 1000, Qc: 10, D1: 3, D2: 5, Pd: 1000000000, C: 7, Kd: 3,
	}
}

func newManager(t *testing.T, cfg Config) *Manager {
	t.Helper()
	m, err := NewManager(cfg)
	if err != nil {
		t.Fatalf("NewManager(%+v): %v", cfg, err)
	}
	return m
}

// TestH1BranchesEquality covers both H1 branches with exact equality.
func TestH1BranchesEquality(t *testing.T) {
	cfg := baseConfig()
	cfg.D1 = 100 // nothing installs during the test
	m := newManager(t, cfg)

	// A-branch: i == A1 exactly triggers; i == A1-1 does not.
	for now := int64(1); now <= 4; now++ {
		mustCall(t, m, 0, 0, now)
	}
	if len(m.queue) != 0 {
		t.Fatalf("enqueued at i=4 < A1=5")
	}
	mustCall(t, m, 0, 0, 5) // i=5 == A1
	if len(m.queue) != 1 || m.queue[0].target != 1 {
		t.Fatalf("A-branch equality: queue=%+v, want one tier-1 job", m.queue)
	}

	// M/B branch: i == M1 and i+b == B1 exactly.
	mustCall(t, m, 1, 9, 6) // i=1 b=9 sum=10
	mustCall(t, m, 1, 0, 7) // i=2 b=9 sum=11
	if len(m.queue) != 1 {
		t.Fatalf("enqueued before M/B equality")
	}
	mustCall(t, m, 1, 0, 8) // i=3 b=9 sum=12 == B1
	if len(m.queue) != 2 || m.queue[1].target != 1 {
		t.Fatalf("M/B-branch equality: queue=%+v, want second tier-1 job", m.queue)
	}
}

// TestH2BranchesEqualityAndJump covers both H2 branches with equality and
// the tier-0 -> tier-2 jump, including H1 and H2 holding simultaneously.
func TestH2BranchesEqualityAndJump(t *testing.T) {
	cfg := baseConfig()
	cfg.A1 = 1000000000 // H1 A-branch unreachable
	cfg.M1 = 1000000000
	cfg.B1 = 1000000000
	m := newManager(t, cfg)

	// H2 A-branch equality: i == A2 jumps from tier 0 to tier 2.
	for now := int64(1); now <= 9; now++ {
		mustCall(t, m, 0, 0, now)
	}
	if len(m.queue) != 0 {
		t.Fatalf("enqueued at i=9 < A2=10")
	}
	mustCall(t, m, 0, 0, 10) // i=10 == A2
	if len(m.queue) != 1 || m.queue[0].target != 2 {
		t.Fatalf("H2 A-branch jump: queue=%+v, want one tier-2 job", m.queue)
	}

	// H2 M/B branch equality: A2-branch unreachable, i == M2 and i+b == B2 exactly.
	cfg2 := baseConfig()
	cfg2.A1 = 1000000000
	cfg2.M1 = 1000000000
	cfg2.B1 = 1000000000
	cfg2.A2 = 1000000000
	cfg2.M2 = 4
	cfg2.B2 = 20
	m2 := newManager(t, cfg2)
	mustCall(t, m2, 0, 4, 1) // i=1 b=4 sum=5
	mustCall(t, m2, 0, 4, 2) // i=2 b=8 sum=10
	mustCall(t, m2, 0, 4, 3) // i=3 b=12 sum=15
	if len(m2.queue) != 0 {
		t.Fatalf("enqueued before H2 M/B equality")
	}
	mustCall(t, m2, 0, 4, 4) // i=4 == M2, b=16, i+b=20 == B2
	if len(m2.queue) != 1 || m2.queue[0].target != 2 {
		t.Fatalf("H2 M/B-branch jump: queue=%+v, want one tier-2 job", m2.queue)
	}

	// H1 and H2 both hold at tier 0: target must be tier 2 (jump).
	cfg3 := baseConfig()
	cfg3.A1 = 6
	cfg3.M1 = 6
	cfg3.B1 = 1000000000
	cfg3.A2 = 6
	cfg3.M2 = 6
	cfg3.B2 = 1000000000
	m3 := newManager(t, cfg3)
	for now := int64(1); now <= 6; now++ {
		mustCall(t, m3, 0, 0, now)
	}
	if len(m3.queue) != 1 || m3.queue[0].target != 2 {
		t.Fatalf("H1&H2 simultaneously: queue=%+v, want one tier-2 job", m3.queue)
	}
}

// TestScalingBoundary covers s = 1 + q/F at q = F-1 versus q = F, and the
// start-time backoff when LF > now.
func TestScalingBoundary(t *testing.T) {
	cfg := baseConfig()
	cfg.A1, cfg.M1, cfg.B1 = 2, 2, 1000000000
	cfg.F = 3
	cfg.D1 = 100
	m := newManager(t, cfg)

	// q=0,1,2 -> s=1: methods 0..2 each promote at i=2.
	mustCall(t, m, 0, 0, 1)
	mustCall(t, m, 0, 0, 2) // enqueue, start 2 finish 102
	mustCall(t, m, 1, 0, 3)
	mustCall(t, m, 1, 0, 4) // q=1, s=1: enqueue, start 102 finish 202
	mustCall(t, m, 2, 0, 5)
	mustCall(t, m, 2, 0, 6) // q=2=F-1, s=1: enqueue, start 202 finish 302
	if len(m.queue) != 3 {
		t.Fatalf("qlen=%d, want 3", len(m.queue))
	}
	wantStarts := []int64{2, 102, 202}
	for i, j := range m.queue {
		if j.start != wantStarts[i] || j.finish != j.start+100 {
			t.Fatalf("job %d: start=%d finish=%d, want start=%d", i, j.start, j.finish, wantStarts[i])
		}
	}

	// q=3=F -> s=2: i=2 < A1*s=4, no promotion.
	mustCall(t, m, 3, 0, 7)
	mustCall(t, m, 3, 0, 8)
	if len(m.queue) != 3 {
		t.Fatalf("promoted at q=F with i=2 < 2*A1")
	}
	// i reaches 4 == A1*s: promotes, start = max(now, LF) = LF = 302.
	mustCall(t, m, 3, 0, 9)
	mustCall(t, m, 3, 0, 10)
	if len(m.queue) != 4 {
		t.Fatalf("qlen=%d, want 4 at i=4 == A1*s", len(m.queue))
	}
	if j := m.queue[3]; j.start != 302 || j.finish != 402 {
		t.Fatalf("backoff: start=%d finish=%d, want 302/402", j.start, j.finish)
	}
}

// TestQueueCapacityDrop covers enqueue at q = Qc-1 and drop at q = Qc.
func TestQueueCapacityDrop(t *testing.T) {
	cfg := baseConfig()
	cfg.A1, cfg.M1, cfg.B1 = 2, 2, 1000000000
	cfg.Qc = 2
	cfg.D1 = 100
	m := newManager(t, cfg)

	mustCall(t, m, 0, 0, 1)
	mustCall(t, m, 0, 0, 2) // q=0 -> enqueue
	mustCall(t, m, 1, 0, 3)
	mustCall(t, m, 1, 0, 4) // q=1=Qc-1 -> enqueue
	if len(m.queue) != 2 {
		t.Fatalf("qlen=%d, want 2", len(m.queue))
	}
	mustCall(t, m, 2, 0, 5)
	mustCall(t, m, 2, 0, 6) // q=2=Qc -> dropped
	mustCall(t, m, 2, 0, 7) // still hot, dropped again
	if got := m.Dropped(); got != 2 {
		t.Fatalf("Dropped=%d, want 2", got)
	}
	if len(m.queue) != 2 {
		t.Fatalf("qlen=%d after drops, want 2", len(m.queue))
	}
}

// TestStartTimeNow covers start = now when LF < now.
func TestStartTimeNow(t *testing.T) {
	cfg := baseConfig()
	cfg.A1, cfg.M1, cfg.B1 = 2, 2, 1000000000
	cfg.D1 = 5
	m := newManager(t, cfg)

	mustCall(t, m, 0, 0, 1)
	mustCall(t, m, 0, 0, 2) // job: start 2 finish 7
	mustCall(t, m, 1, 0, 100)
	mustCall(t, m, 1, 0, 101) // LF=7 < now: start = 101
	if j := m.queue[0]; j.start != 101 || j.finish != 106 {
		t.Fatalf("start=%d finish=%d, want 101/106", j.start, j.finish)
	}
}

// TestDecay covers multi-epoch decay in one shift, the 62-bit cap, the epoch
// boundary, and counters incremented after decay.
func TestDecay(t *testing.T) {
	newCfg := func() Config {
		cfg := baseConfig()
		cfg.A1, cfg.M1, cfg.B1 = 1000000000, 1000000000, 1000000000
		cfg.A2, cfg.M2, cfg.B2 = 1000000000, 1000000000, 1000000000
		cfg.Pd = 100
		return cfg
	}

	// Multi-epoch: g=4 between now=17 and now=450, applied as one 4-bit shift.
	m := newManager(t, newCfg())
	mustCall(t, m, 0, 0, 1)
	for now := int64(2); now <= 16; now++ {
		mustCall(t, m, 0, 0, now) // i=16
	}
	mustCall(t, m, 0, 3, 17)  // i=17, b=3
	mustCall(t, m, 0, 1, 450) // g=4: i=17>>4=1, b=3>>4=0; then i=2, b=1
	st := mustState(t, m, 0, 450)
	if st.I != 2 || st.B != 1 {
		t.Fatalf("multi-epoch decay: i=%d b=%d, want 2/1", st.I, st.B)
	}

	// Epoch boundary: now exactly a multiple of Pd belongs to the new epoch.
	m2 := newManager(t, newCfg())
	mustCall(t, m2, 0, 0, 99)  // epoch 0, i=1
	mustCall(t, m2, 0, 0, 100) // epoch 1, g=1: i=1>>1=0, then i=1
	st = mustState(t, m2, 0, 100)
	if st.I != 1 {
		t.Fatalf("epoch boundary: i=%d, want 1", st.I)
	}
	mustCall(t, m2, 0, 0, 199) // same epoch, g=0: i=2
	mustCall(t, m2, 0, 0, 200) // epoch 2, g=1: i=2>>1=1, then i=2
	st = mustState(t, m2, 0, 200)
	if st.I != 2 {
		t.Fatalf("epoch boundary 2: i=%d, want 2", st.I)
	}

	// Decay applies before this call's own increments (b included).
	m3 := newManager(t, newCfg())
	mustCall(t, m3, 0, 7, 1)   // i=1, b=7
	mustCall(t, m3, 0, 1, 200) // g=2: i=1>>2=0, b=7>>2=1; then i=1, b=2
	st = mustState(t, m3, 0, 200)
	if st.I != 1 || st.B != 2 {
		t.Fatalf("decay then add: i=%d b=%d, want 1/2", st.I, st.B)
	}
}

// TestDecayCap62 verifies the shift amount is capped at 62 in a single shift.
func TestDecayCap62(t *testing.T) {
	ms := methodState{i: 1 << 62, b: 1<<62 + 3}
	decayView(&ms, 1000, 1) // g=1000 -> capped at 62
	if ms.i != 1 || ms.b != 1 {
		t.Fatalf("cap 62: i=%d b=%d, want 1/1", ms.i, ms.b)
	}
	if ms.epoch != 1000 {
		t.Fatalf("epoch=%d, want 1000", ms.epoch)
	}
	ms2 := methodState{i: 1 << 62, b: 1 << 62}
	decayView(&ms2, 62, 1) // g=62 exactly
	if ms2.i != 1 || ms2.b != 1 {
		t.Fatalf("g=62: i=%d b=%d, want 1/1", ms2.i, ms2.b)
	}
	ms3 := methodState{i: 1 << 62, b: 1 << 62}
	decayView(&ms3, 63, 1) // g=63 -> capped: same as 62
	if ms3.i != 1 || ms3.b != 1 {
		t.Fatalf("g=63 capped: i=%d b=%d, want 1/1", ms3.i, ms3.b)
	}
}

// TestDeoptPenaltyLinear covers d=1+dc scaling, cu=now+C*dc growth,
// re-accumulation after deopt, and cooldown blocking tier-2 promotion until
// now == cu exactly.
func TestDeoptPenaltyLinear(t *testing.T) {
	cfg := baseConfig()
	cfg.N = 1
	cfg.A1, cfg.M1, cfg.B1 = 1000000000, 1000000000, 1000000000
	cfg.A2, cfg.M2, cfg.B2 = 6, 6, 1000000000 // B2 huge: only the A2 branch can fire
	cfg.C = 30
	cfg.Kd = 5
	cfg.D2 = 1
	m := newManager(t, cfg)

	// dc=0: H2 at i >= 6.
	for now := int64(1); now <= 6; now++ {
		mustCall(t, m, 0, 0, now)
	}
	if len(m.queue) != 1 || m.queue[0].target != 2 {
		t.Fatalf("dc=0: want tier-2 job, queue=%+v", m.queue)
	}
	if got := mustCall(t, m, 0, 0, 7); got != 2 { // finish 6+1=7
		t.Fatalf("tier=%d, want 2", got)
	}
	if err := m.Deopt(0, 8); err != nil {
		t.Fatalf("deopt 1: %v", err)
	}
	st := mustState(t, m, 0, 8)
	if st.Dc != 1 || st.Cu != 38 || st.I != 0 || st.B != 0 {
		t.Fatalf("after deopt 1: %+v, want dc=1 cu=38 i=0 b=0", st)
	}
	// dc=1: H2 needs i >= 12; cooldown until 38 blocks it even when hot.
	for now := int64(9); now <= 37; now++ {
		mustCall(t, m, 0, 0, now) // i=29 at now=37, well past 12
	}
	if len(m.queue) != 0 {
		t.Fatalf("promoted during cooldown")
	}
	mustCall(t, m, 0, 0, 38) // now == cu: i=30 >= 12 -> enqueue tier 2
	if len(m.queue) != 1 || m.queue[0].target != 2 {
		t.Fatalf("dc=1 at cu: want tier-2 job, queue=%+v", m.queue)
	}
	if got := mustCall(t, m, 0, 0, 39); got != 2 { // finish 38+1=39
		t.Fatalf("tier=%d, want 2", got)
	}
	if err := m.Deopt(0, 40); err != nil {
		t.Fatalf("deopt 2: %v", err)
	}
	st = mustState(t, m, 0, 40)
	if st.Dc != 2 || st.Cu != 100 {
		t.Fatalf("after deopt 2: %+v, want dc=2 cu=100", st)
	}
	// dc=2: H2 needs i >= 18; cooldown until 100.
	for now := int64(41); now <= 99; now++ {
		mustCall(t, m, 0, 0, now) // i=59 at now=99
	}
	if len(m.queue) != 0 {
		t.Fatalf("promoted during cooldown 2")
	}
	mustCall(t, m, 0, 0, 100) // now == cu: i=60 >= 18 -> enqueue
	if len(m.queue) != 1 {
		t.Fatalf("dc=2 at cu: want tier-2 job, queue=%+v", m.queue)
	}
}

// TestDeoptThresholdExactness checks the exact i at which H2 fires for each dc.
func TestDeoptThresholdExactness(t *testing.T) {
	cfg := baseConfig()
	cfg.N = 1
	cfg.A1, cfg.M1, cfg.B1 = 1000000000, 1000000000, 1000000000
	cfg.A2, cfg.M2, cfg.B2 = 6, 6, 1000000000
	cfg.C = 1 // tiny cooldown: cu = now + dc
	cfg.Kd = 100
	cfg.D2 = 1
	m := newManager(t, cfg)

	now := int64(0)
	call := func() { now++; mustCall(t, m, 0, 0, now) }
	for dc := int64(0); dc < 4; dc++ {
		threshold := 6 * (dc + 1)
		// Climb; the job must appear exactly when i reaches threshold,
		// and never during cooldown.
		for i := int64(1); i <= threshold; i++ {
			call()
			if now <= m.ms[0].cu && len(m.queue) != 0 {
				t.Fatalf("dc=%d: enqueued during cooldown at now=%d cu=%d", dc, now, m.ms[0].cu)
			}
			if now > m.ms[0].cu && i < threshold && len(m.queue) != 0 {
				t.Fatalf("dc=%d: enqueued at i=%d < %d", dc, i, threshold)
			}
		}
		if len(m.queue) != 1 {
			t.Fatalf("dc=%d: no job at i=%d", dc, threshold)
		}
		call() // installs the tier-2 job (D2=1)
		if m.ms[0].tier != 2 {
			t.Fatalf("dc=%d: tier=%d, want 2", dc, m.ms[0].tier)
		}
		now++
		if err := m.Deopt(0, now); err != nil {
			t.Fatalf("dc=%d: deopt: %v", dc, err)
		}
		if m.ms[0].cu != now+(dc+1) {
			t.Fatalf("dc=%d: cu=%d, want %d", dc, m.ms[0].cu, now+dc+1)
		}
		// Jump far ahead: decay (g capped at 62) resets i and b to 0 and
		// the cooldown has long expired.
		now += 63 * cfg.Pd
	}
}

// TestStateReadOnly verifies State never mutates anything, including jobs it
// would install and counters it would decay.
func TestStateReadOnly(t *testing.T) {
	m := newManager(t, exampleConfig())
	mustCall(t, m, 0, 0, 1)
	mustCall(t, m, 0, 0, 2)
	mustCall(t, m, 0, 0, 3) // tier-1 job, finish 8

	beforeT, beforeLF, beforeDropped := m.tNow, m.lf, m.dropped
	beforeChecks, beforeInstalls := m.headChecks, m.installs
	beforeQueue := append([]job(nil), m.queue...)
	beforeMs := append([]methodState(nil), m.ms...)

	st1 := mustState(t, m, 0, 1000) // would install + decay if it mutated
	st2 := mustState(t, m, 0, 1000)
	if st1 != st2 {
		t.Fatalf("State not repeatable: %+v vs %+v", st1, st2)
	}
	if st1.Tier != 1 {
		t.Fatalf("State view tier=%d, want 1 (install view)", st1.Tier)
	}
	if m.tNow != beforeT || m.lf != beforeLF || m.dropped != beforeDropped ||
		m.headChecks != beforeChecks || m.installs != beforeInstalls {
		t.Fatalf("State mutated globals")
	}
	if len(m.queue) != len(beforeQueue) || m.queue[0] != beforeQueue[0] {
		t.Fatalf("State mutated queue")
	}
	for i := range m.ms {
		if m.ms[i] != beforeMs[i] {
			t.Fatalf("State mutated method %d", i)
		}
	}
	// The queued job is still installed later by an accepted Call.
	if got := mustCall(t, m, 0, 0, 1000); got != 1 {
		t.Fatalf("tier=%d, want 1 (job installed by Call)", got)
	}
}

// TestHeadCheckBound verifies the queue head is inspected at most
// installs+1 times per accepted operation.
func TestHeadCheckBound(t *testing.T) {
	cfg := baseConfig()
	cfg.A1, cfg.M1, cfg.B1 = 1, 1, 1000000000
	cfg.D1 = 10
	m := newManager(t, cfg)

	delta := func(op func(), wantInstalls int64) {
		t.Helper()
		prevChecks, prevInstalls := m.headChecks, m.installs
		op()
		dC, dI := m.headChecks-prevChecks, m.installs-prevInstalls
		if dC > dI+1 {
			t.Fatalf("head checks %d exceed installs+1 (%d+1)", dC, dI)
		}
		if dI != wantInstalls {
			t.Fatalf("installed %d jobs, want %d", dI, wantInstalls)
		}
	}

	// Space calls so LF does not chain: finishes land at 11, 22, 33.
	delta(func() { mustCall(t, m, 0, 0, 1) }, 0)  // enqueue finish 11
	delta(func() { mustCall(t, m, 1, 0, 12) }, 1) // installs 11, enqueue finish 22
	delta(func() { mustCall(t, m, 2, 0, 23) }, 1) // installs 22, enqueue finish 33
	delta(func() { mustCall(t, m, 3, 0, 40) }, 1) // installs 33, enqueue finish 50
	// Two pending jobs install in one op: checks = installs+1.
	mustCall(t, m, 4, 0, 51)                      // installs 50, enqueue finish 61
	mustCall(t, m, 5, 0, 52)                      // enqueue finish 71 (LF chains)
	delta(func() { mustCall(t, m, 6, 0, 71) }, 2) // installs both
}

// TestConcurrentSmoke hammers the manager from many goroutines; -race
// validates mutual exclusion and the invariants are checked at the end.
func TestConcurrentSmoke(t *testing.T) {
	cfg := exampleConfig()
	cfg.N = 16
	m := newManager(t, cfg)

	var clock atomic.Int64
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for k := 0; k < 500; k++ {
				now := clock.Add(1)
				method := (g*7 + k) % cfg.N
				switch k % 3 {
				case 0:
					_, _ = m.Call(method, int64(k%5), now)
				case 1:
					_ = m.Deopt(method, now)
				default:
					_, _ = m.State(method, now)
				}
			}
		}(g)
	}
	wg.Wait()

	// Invariants: queue capacity, monotonic finish times, tier range,
	// non-negative counters, single inflight job per method.
	if len(m.queue) > cfg.Qc {
		t.Fatalf("queue len %d exceeds Qc", len(m.queue))
	}
	for i := 1; i < len(m.queue); i++ {
		if m.queue[i].finish < m.queue[i-1].finish {
			t.Fatalf("finish times not monotone at %d", i)
		}
	}
	inflight := map[int]int{}
	for _, j := range m.queue {
		inflight[j.method]++
		if inflight[j.method] > 1 {
			t.Fatalf("method %d has multiple inflight jobs", j.method)
		}
	}
	for id, ms := range m.ms {
		if ms.tier < 0 || ms.tier > 2 || ms.i < 0 || ms.b < 0 || ms.dc < 0 {
			t.Fatalf("method %d in bad state: %+v", id, ms)
		}
	}
}
