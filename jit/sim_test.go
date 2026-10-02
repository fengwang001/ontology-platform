package jit

// This file holds an independent naive implementation of the specification
// (full queue scans on every operation, bit-by-bit decay) plus a randomized
// differential test replaying 2000 random operation sequences against the
// real Manager, and a replay-determinism check.

import (
	"errors"
	"fmt"
	"math/big"
	"math/rand"
	"strings"
	"testing"
)

type naiveMethod struct {
	tier     int
	i        int64
	b        int64
	dc       int64
	epoch    int64
	cu       int64
	inflight bool
}

type naiveSim struct {
	cfg     Config
	ms      []naiveMethod
	tNow    int64
	jobs    []job // pending only; finished jobs are removed by full scans
	lf      int64
	dropped int64
}

func newNaive(cfg Config) *naiveSim {
	return &naiveSim{cfg: cfg, ms: make([]naiveMethod, cfg.N)}
}

// install scans the whole queue and applies every job with finish <= now,
// preserving enqueue order.
func (s *naiveSim) install(now int64) {
	kept := s.jobs[:0]
	for _, j := range s.jobs {
		if j.finish <= now {
			s.ms[j.method].tier = j.target
			s.ms[j.method].inflight = false
		} else {
			kept = append(kept, j)
		}
	}
	s.jobs = kept
}

// decay halves the counters one bit at a time, at most 62 times.
func (s *naiveSim) decay(m *naiveMethod, now int64) {
	epoch := now / s.cfg.Pd
	g := epoch - m.epoch
	if g > 62 {
		g = 62
	}
	for k := int64(0); k < g; k++ {
		m.i /= 2
		m.b /= 2
	}
	m.epoch = epoch
}

// naiveGeq reports x >= a*b*c using exact big-integer arithmetic.
func naiveGeq(x, a, b, c int64) bool {
	p := new(big.Int).Mul(big.NewInt(a), big.NewInt(b))
	p.Mul(p, big.NewInt(c))
	return big.NewInt(x).Cmp(p) >= 0
}

func (s *naiveSim) h1(m *naiveMethod, scale int64) bool {
	if naiveGeq(m.i, s.cfg.A1, scale, 1) {
		return true
	}
	return naiveGeq(m.i, s.cfg.M1, scale, 1) && naiveGeq(m.i+m.b, s.cfg.B1, scale, 1)
}

func (s *naiveSim) h2(m *naiveMethod, d, scale int64) bool {
	if naiveGeq(m.i, s.cfg.A2, d, scale) {
		return true
	}
	return naiveGeq(m.i, s.cfg.M2, d, scale) && naiveGeq(m.i+m.b, s.cfg.B2, d, scale)
}

// call runs the naive Call and returns the tier plus a human-readable
// explanation of the promotion decision.
func (s *naiveSim) call(method int, n, now int64) (int, error, string) {
	if method < 0 || method >= s.cfg.N || n < 0 || n > 1000000 || now < 0 || now > maxNow {
		return 0, ErrInvalidArgument, "invalid argument"
	}
	if now < s.tNow {
		return 0, ErrClockRegression, "clock regression"
	}
	s.install(now)
	m := &s.ms[method]
	s.decay(m, now)
	tier := m.tier
	m.i++
	m.b += n

	reason := "no promotion"
	if m.tier >= 2 {
		reason = "already tier 2"
	} else if m.inflight {
		reason = "inflight job"
	} else if now < m.cu {
		reason = fmt.Sprintf("cooldown now=%d < cu=%d", now, m.cu)
	} else {
		q := int64(len(s.jobs))
		scale := 1 + q/s.cfg.F
		d := 1 + m.dc
		h2 := m.dc < s.cfg.Kd && s.h2(m, d, scale)
		target := 0
		switch m.tier {
		case 0:
			if h2 {
				target = 2
			} else if s.h1(m, scale) {
				target = 1
			}
		case 1:
			if h2 {
				target = 2
			}
		}
		reason = fmt.Sprintf("i=%d b=%d q=%d s=%d d=%d h1=%v h2=%v target=%d",
			m.i, m.b, q, scale, d, s.h1(m, scale), h2, target)
		if target != 0 {
			if q >= int64(s.cfg.Qc) {
				s.dropped++
				reason += " dropped(queue full)"
			} else {
				start := now
				if s.lf > start {
					start = s.lf
				}
				dur := s.cfg.D1
				if target == 2 {
					dur = s.cfg.D2
				}
				finish := start + dur
				s.jobs = append(s.jobs, job{method: method, target: target, start: start, finish: finish})
				s.lf = finish
				m.inflight = true
				reason += fmt.Sprintf(" enqueued(start=%d finish=%d)", start, finish)
			}
		}
	}
	s.tNow = now
	return tier, nil, reason
}

func (s *naiveSim) deopt(method int, now int64) error {
	if method < 0 || method >= s.cfg.N || now < 0 || now > maxNow {
		return ErrInvalidArgument
	}
	if now < s.tNow {
		return ErrClockRegression
	}
	// Determine the post-install tier without mutating, so a rejected
	// deopt changes nothing.
	tier := s.ms[method].tier
	for _, j := range s.jobs {
		if j.finish <= now && j.method == method {
			tier = j.target
		}
	}
	if tier != 2 {
		return ErrNotTier2
	}
	s.install(now)
	m := &s.ms[method]
	s.decay(m, now)
	m.tier = 0
	m.i = 0
	m.b = 0
	m.dc++
	m.cu = now + s.cfg.C*m.dc
	s.tNow = now
	return nil
}

func (s *naiveSim) state(method int, now int64) (State, error) {
	if method < 0 || method >= s.cfg.N || now < 0 || now > maxNow {
		return State{}, ErrInvalidArgument
	}
	if now < s.tNow {
		return State{}, ErrClockRegression
	}
	m := s.ms[method]
	qlen := 0
	for _, j := range s.jobs {
		if j.finish <= now {
			if j.method == method {
				m.tier = j.target
			}
		} else {
			qlen++
		}
	}
	epoch := now / s.cfg.Pd
	g := epoch - m.epoch
	if g > 62 {
		g = 62
	}
	for k := int64(0); k < g; k++ {
		m.i /= 2
		m.b /= 2
	}
	return State{Tier: m.tier, I: m.i, B: m.b, Dc: m.dc, Cu: m.cu, QueueLen: qlen, LF: s.lf}, nil
}

// dump returns a canonical snapshot of the whole manager state.
func dumpManager(m *Manager) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "T=%d LF=%d dropped=%d\n", m.tNow, m.lf, m.dropped)
	for id, ms := range m.ms {
		fmt.Fprintf(&sb, "m%d{t=%d i=%d b=%d dc=%d e=%d cu=%d f=%v}\n",
			id, ms.tier, ms.i, ms.b, ms.dc, ms.epoch, ms.cu, ms.inflight)
	}
	for _, j := range m.queue {
		fmt.Fprintf(&sb, "job{m=%d t=%d s=%d f=%d}\n", j.method, j.target, j.start, j.finish)
	}
	return sb.String()
}

func dumpNaive(s *naiveSim) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "T=%d LF=%d dropped=%d\n", s.tNow, s.lf, s.dropped)
	for id, ms := range s.ms {
		fmt.Fprintf(&sb, "m%d{t=%d i=%d b=%d dc=%d e=%d cu=%d f=%v}\n",
			id, ms.tier, ms.i, ms.b, ms.dc, ms.epoch, ms.cu, ms.inflight)
	}
	for _, j := range s.jobs {
		fmt.Fprintf(&sb, "job{m=%d t=%d s=%d f=%d}\n", j.method, j.target, j.start, j.finish)
	}
	return sb.String()
}

type opKind int

const (
	opCall opKind = iota
	opDeopt
	opState
)

type testOp struct {
	kind   opKind
	method int
	n      int64
	now    int64
}

func (o testOp) String() string {
	switch o.kind {
	case opCall:
		return fmt.Sprintf("Call(%d, n=%d, now=%d)", o.method, o.n, o.now)
	case opDeopt:
		return fmt.Sprintf("Deopt(%d, now=%d)", o.method, o.now)
	default:
		return fmt.Sprintf("State(%d, now=%d)", o.method, o.now)
	}
}

// randomConfig draws small parameters so promotions, decays, drops and
// deopts all happen frequently.
func randomConfig(rng *rand.Rand) Config {
	pick := func(hi int64) int64 { return 1 + rng.Int63n(hi) }
	a1 := pick(12)
	a2 := pick(16)
	cfg := Config{
		N:  1 + rng.Intn(6),
		A1: a1,
		M1: 1 + rng.Int63n(a1),
		B1: pick(30),
		A2: a2,
		M2: 1 + rng.Int63n(a2),
		B2: pick(40),
		F:  pick(4),
		Qc: 1 + rng.Intn(3),
		D1: pick(30),
		D2: pick(30),
		Pd: pick(8),
		C:  pick(10),
		Kd: pick(3),
	}
	return cfg
}

// randomOps draws a mostly monotone operation sequence with occasional
// invalid arguments and clock regressions.
func randomOps(rng *rand.Rand, cfg Config, count int) []testOp {
	ops := make([]testOp, 0, count)
	var now int64
	for k := 0; k < count; k++ {
		now += rng.Int63n(12)
		op := testOp{now: now, method: rng.Intn(cfg.N)}
		switch r := rng.Intn(100); {
		case r < 55:
			op.kind = opCall
			op.n = rng.Int63n(12)
		case r < 70:
			op.kind = opDeopt
		default:
			op.kind = opState
		}
		// Inject invalid operations.
		switch bad := rng.Intn(100); {
		case bad < 2:
			op.method = cfg.N + rng.Intn(2) // out-of-range method
		case bad < 4:
			op.now-- // clock regression (unless now == 0)
			if rng.Intn(2) == 0 {
				op.now = -1 // invalid argument instead
			}
		case bad < 5 && op.kind == opCall:
			op.n = 1000001 // invalid n
		}
		ops = append(ops, op)
	}
	return ops
}

// TestDifferential replays 2000 random sequences against the naive
// simulation, comparing every return value, error and full state dump.
func TestDifferential(t *testing.T) {
	const sequences = 2000
	var acceptedCalls, acceptedDeopts, rejectedOps, enqueued, installsTotal, droppedTotal int
	for seq := 0; seq < sequences; seq++ {
		rng := rand.New(rand.NewSource(int64(seq)*7919 + 13))
		cfg := randomConfig(rng)
		ops := randomOps(rng, cfg, 30+rng.Intn(90))

		m, err := NewManager(cfg)
		if err != nil {
			t.Fatalf("seq %d: NewManager(%+v): %v", seq, cfg, err)
		}
		replay, err := NewManager(cfg)
		if err != nil {
			t.Fatalf("seq %d: NewManager replay: %v", seq, err)
		}
		sim := newNaive(cfg)

		verbose := seq < 2 // full op traces for the first sequences
		if verbose {
			t.Logf("seq %d config=%+v ops=%d", seq, cfg, len(ops))
		}
		fail := false
		for k, op := range ops {
			prevChecks, prevInstalls := m.headChecks, m.installs
			prevQLen := len(m.queue)
			var gotErr, wantErr error
			var basis string
			switch op.kind {
			case opCall:
				gotTier, ge := m.Call(op.method, op.n, op.now)
				replayTier, re := replay.Call(op.method, op.n, op.now)
				wantTier, we, why := sim.call(op.method, op.n, op.now)
				gotErr, wantErr, basis = ge, we, why
				if ge == nil && we == nil && gotTier != wantTier {
					t.Errorf("seq %d op %d %s: tier=%d, want %d", seq, k, op, gotTier, wantTier)
					fail = true
				}
				if ge == nil && gotTier != replayTier {
					t.Errorf("seq %d op %d %s: replay tier=%d != %d", seq, k, op, replayTier, gotTier)
					fail = true
				}
				_ = re
			case opDeopt:
				gotErr = m.Deopt(op.method, op.now)
				re := replay.Deopt(op.method, op.now)
				wantErr = sim.deopt(op.method, op.now)
				basis = "deopt"
				if (gotErr == nil) != (re == nil) {
					t.Errorf("seq %d op %d %s: replay err=%v != %v", seq, k, op, re, gotErr)
					fail = true
				}
			case opState:
				gotSt, ge := m.State(op.method, op.now)
				replaySt, re := replay.State(op.method, op.now)
				wantSt, we := sim.state(op.method, op.now)
				gotErr, wantErr, basis = ge, we, "state view"
				if ge == nil && we == nil && gotSt != wantSt {
					t.Errorf("seq %d op %d %s: state=%+v, want %+v", seq, k, op, gotSt, wantSt)
					fail = true
				}
				if ge == nil && gotSt != replaySt {
					t.Errorf("seq %d op %d %s: replay state=%+v != %+v", seq, k, op, replaySt, gotSt)
					fail = true
				}
				_ = re
			}
			switch {
			case gotErr != nil:
				rejectedOps++
			case op.kind == opCall:
				acceptedCalls++
			case op.kind == opDeopt:
				acceptedDeopts++
			}
			if len(m.queue) > prevQLen {
				enqueued++
			}
			installsTotal += int(m.installs - prevInstalls)
			if !errors.Is(gotErr, wantErr) && (gotErr == nil) != (wantErr == nil) {
				t.Errorf("seq %d op %d %s: err=%v, want %v", seq, k, op, gotErr, wantErr)
				fail = true
			} else if gotErr != nil && wantErr != nil && !errors.Is(gotErr, wantErr) {
				t.Errorf("seq %d op %d %s: err=%v, want %v", seq, k, op, gotErr, wantErr)
				fail = true
			}
			if verbose {
				t.Logf("seq %d op %d: %s -> err=%v basis: %s", seq, k, op, gotErr, basis)
			}
			// Head-check bound on accepted operations.
			if gotErr == nil && op.kind != opState {
				if dC, dI := m.headChecks-prevChecks, m.installs-prevInstalls; dC > dI+1 {
					t.Errorf("seq %d op %d %s: head checks %d > installs %d + 1", seq, k, op, dC, dI)
					fail = true
				}
			}
			// Invariants after every operation.
			if len(m.queue) > cfg.Qc {
				t.Errorf("seq %d op %d: queue len %d > Qc %d", seq, k, len(m.queue), cfg.Qc)
				fail = true
			}
			for j := 1; j < len(m.queue); j++ {
				if m.queue[j].finish < m.queue[j-1].finish {
					t.Errorf("seq %d op %d: finish times not monotone", seq, k)
					fail = true
				}
			}
			if fail {
				t.Logf("seq %d op %d %s FAILED\nmanager:\n%s\nnaive:\n%s",
					seq, k, op, dumpManager(m), dumpNaive(sim))
				t.FailNow()
			}
		}
		// Final full-state comparison: manager vs naive vs replay.
		dm, ds, dr := dumpManager(m), dumpNaive(sim), dumpManager(replay)
		if dm != ds {
			t.Fatalf("seq %d final state mismatch\nmanager:\n%s\nnaive:\n%s", seq, dm, ds)
		}
		if dm != dr {
			t.Fatalf("seq %d replay mismatch\nmanager:\n%s\nreplay:\n%s", seq, dm, dr)
		}
		if verbose {
			t.Logf("seq %d final:\n%s", seq, dm)
		}
		droppedTotal += int(m.dropped)
	}
	t.Logf("coverage: acceptedCalls=%d acceptedDeopts=%d rejectedOps=%d enqueuedJobs=%d installs=%d droppedJobs=%d",
		acceptedCalls, acceptedDeopts, rejectedOps, enqueued, installsTotal, droppedTotal)
}
