package sampler

import (
	"errors"
	"fmt"
	"math/rand"
	"testing"

	"ontology/tailsampler/policy"
)

func TestWindowRollover(t *testing.T) {
	p := baseParams()
	p.Wb, p.W, p.P, p.L, p.Nmax = 10, 5, 10000, 1e9, 10
	s := mustNew(t, p)
	mustIngest(t, s, 0, "t1", "s1", 1, false)
	mustIngest(t, s, 1, "t2", "s1", 1, false)
	d := mustTick(t, s, 6) // window 0: t1 Prob (u=1), t2 Budget
	if len(d) != 2 {
		t.Fatalf("Tick(6) decisions = %d, want 2", len(d))
	}
	wantDecision(t, d[0], "t1", true, policy.ReasonProb, 1, 6, false)
	wantDecision(t, d[1], "t2", false, policy.ReasonBudget, 1, 6, false)
	mustIngest(t, s, 12, "t3", "s1", 1, false)
	d = mustTick(t, s, 17) // window 1: u resets, t3 Prob
	if len(d) != 1 {
		t.Fatalf("Tick(17) decisions = %d, want 1", len(d))
	}
	wantDecision(t, d[0], "t3", true, policy.ReasonProb, 1, 17, false)
}

func TestErrorExceedsQuota(t *testing.T) {
	p := baseParams()
	p.P, p.L, p.W, p.Nmax = 10000, 1e9, 5, 10
	s := mustNew(t, p)
	mustIngest(t, s, 0, "e1", "s1", 1, true)
	mustIngest(t, s, 1, "e2", "s1", 1, true)
	mustIngest(t, s, 2, "p1", "s1", 1, false)
	d := mustTick(t, s, 7)
	if len(d) != 3 {
		t.Fatalf("Tick(7) decisions = %d, want 3", len(d))
	}
	wantDecision(t, d[0], "e1", true, policy.ReasonError, 1, 7, false)
	wantDecision(t, d[1], "e2", true, policy.ReasonError, 1, 7, false) // u=2 > Q=1
	wantDecision(t, d[2], "p1", false, policy.ReasonBudget, 1, 7, false)
}

func TestQZeroAndPBounds(t *testing.T) {
	p := baseParams()
	p.Q, p.P, p.L, p.W, p.Nmax = 0, 10000, 1e9, 5, 10
	s := mustNew(t, p)
	mustIngest(t, s, 0, "plain", "s1", 1, false)
	mustIngest(t, s, 1, "err", "s1", 1, true)
	d := mustTick(t, s, 6)
	if len(d) != 2 {
		t.Fatalf("Tick(6) decisions = %d, want 2", len(d))
	}
	wantDecision(t, d[0], "plain", false, policy.ReasonBudget, 1, 6, false) // Q=0
	wantDecision(t, d[1], "err", true, policy.ReasonError, 1, 6, false)

	p.P = 0
	s = mustNew(t, p)
	mustIngest(t, s, 0, "zero", "s1", 1, false)
	d = mustTick(t, s, 5)
	wantDecision(t, d[0], "zero", false, policy.ReasonSampledOut, 1, 5, false)

	p.P, p.Q = 10000, 1
	s = mustNew(t, p)
	mustIngest(t, s, 0, "full", "s1", 1, false)
	d = mustTick(t, s, 5)
	wantDecision(t, d[0], "full", true, policy.ReasonProb, 1, 5, false)
}

func TestRejectOrder(t *testing.T) {
	s := mustNew(t, baseParams())
	if _, err := s.Ingest(0, "", "s", 0, false); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("empty traceID: %v", err)
	}
	if _, err := s.Ingest(0, "t", "", 0, false); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("empty spanID: %v", err)
	}
	if _, err := s.Ingest(0, "t", "s", 1e9+1, false); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("durMs overflow: %v", err)
	}
	if _, err := s.Ingest(1e12+1, "t", "s", 0, false); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("now overflow: %v", err)
	}
	mustIngest(t, s, 10, "t1", "s1", 1, false)
	// Both param and clock violated -> param error wins.
	if _, err := s.Ingest(5, "", "s2", 0, false); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("param before clock: %v", err)
	}
	if _, err := s.Ingest(5, "t1", "s2", 0, false); !errors.Is(err, ErrClock) {
		t.Fatalf("clock regression: %v", err)
	}
	// Both clock and duplicate violated -> clock error wins.
	if _, err := s.Ingest(5, "t1", "s1", 0, false); !errors.Is(err, ErrClock) {
		t.Fatalf("clock before duplicate: %v", err)
	}
	if _, err := s.Tick(9); !errors.Is(err, ErrClock) {
		t.Fatalf("tick clock regression: %v", err)
	}
	if _, err := s.Tick(1e12 + 1); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("tick now overflow: %v", err)
	}
}

func TestParamsValidation(t *testing.T) {
	ok := Params{W: 1e9, Sc: 1, Nmax: 1, Td: 1e9, Cmax: 1e6, L: 1e9, P: 10000, Wb: 1, Q: 1e9}
	if _, err := New(ok); err != nil {
		t.Fatalf("boundary-valid params rejected: %v", err)
	}
	var bad []Params
	mutate := func(f func(*Params)) {
		p := ok
		f(&p)
		bad = append(bad, p)
	}
	mutate(func(p *Params) { p.W = 1e9 + 1 })
	mutate(func(p *Params) { p.Td = 1e9 + 1 })
	mutate(func(p *Params) { p.L = 1e9 + 1 })
	mutate(func(p *Params) { p.Sc = 0 })
	mutate(func(p *Params) { p.Sc = 1e4 + 1 })
	mutate(func(p *Params) { p.Nmax = 0 })
	mutate(func(p *Params) { p.Nmax = 1e5 + 1 })
	mutate(func(p *Params) { p.Cmax = 1e6 + 1 })
	mutate(func(p *Params) { p.P = 10001 })
	mutate(func(p *Params) { p.Wb = 0 })
	mutate(func(p *Params) { p.Wb = 1e9 + 1 })
	mutate(func(p *Params) { p.Q = 1e9 + 1 })
	for i, p := range bad {
		if _, err := New(p); !errors.Is(err, ErrInvalidParam) {
			t.Fatalf("bad params #%d (%+v): err = %v", i, p, err)
		}
	}
}

// op is one randomized input for the naive-model comparison test.
type op struct {
	isTick   bool
	now      uint64
	tid, sid string
	dur      uint64
	isErr    bool
}

func genOps(seed int64, count int) []op {
	r := rand.New(rand.NewSource(seed))
	ops := make([]op, 0, count)
	var now uint64
	for i := 0; i < count; i++ {
		now += uint64(r.Intn(7))
		if r.Intn(100) < 20 {
			ops = append(ops, op{isTick: true, now: now})
			continue
		}
		ops = append(ops, op{
			now:   now,
			tid:   fmt.Sprintf("t%d", r.Intn(10)),
			sid:   fmt.Sprintf("s%d", r.Intn(10)),
			dur:   uint64(r.Intn(121)),
			isErr: r.Intn(10) == 0,
		})
	}
	return ops
}

func runOp(s *Sampler, n *naive, o op) (da, db []Decision, ea, eb error) {
	if o.isTick {
		da, ea = s.Tick(o.now)
		db, eb = n.tick(o.now)
	} else {
		da, ea = s.Ingest(o.now, o.tid, o.sid, o.dur, o.isErr)
		db, eb = n.ingest(o.now, o.tid, o.sid, o.dur, o.isErr)
	}
	return
}
