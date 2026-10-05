package plan_test

import (
	"math/rand"
	"reflect"
	"sync"
	"testing"

	"ontology/ingest"
	"ontology/plan"
)

type op struct {
	kind   string // "i"=Ingest(seq=a) "h"=Hello(lo2=a,hi2=b) "p"=Plan
	a, b   int64
	now    int64
	budget int
	err    error
	reqs   []plan.Request
	stats  *ingest.Stats
}

func ingestOps(seqs ...int64) []op {
	var ops []op
	for _, s := range seqs {
		ops = append(ops, op{kind: "i", a: s})
	}
	return ops
}

func runCase(t *testing.T, p ingest.Params, ops []op) {
	t.Helper()
	e, err := ingest.New(p)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := e.Register("d"); err != nil {
		t.Fatalf("Register: %v", err)
	}
	for i, o := range ops {
		var opErr error
		switch o.kind {
		case "i":
			opErr = e.Ingest("d", o.a, o.now)
		case "h":
			opErr = e.Hello("d", o.a, o.b, o.now)
		case "p":
			var reqs []plan.Request
			reqs, opErr = plan.Plan(e, "d", o.now, o.budget)
			if opErr == nil && !reflect.DeepEqual(reqs, o.reqs) {
				t.Fatalf("op %d plan(%d,%d): got %v want %v", i, o.now, o.budget, reqs, o.reqs)
			}
			t.Logf("op=%d plan(now=%d,budget=%d) -> reqs=%v err=%v", i, o.now, o.budget, reqs, opErr)
		}
		if opErr != o.err {
			t.Fatalf("op %d (%v): got err %v want %v", i, o, opErr, o.err)
		}
		st, err := e.Stats("d")
		if err != nil {
			t.Fatalf("op %d stats: %v", i, err)
		}
		if sum := st.Received + st.Lost + st.Missing; sum != st.Hi {
			t.Fatalf("op %d: conservation broken: %+v", i, st)
		}
		if o.stats != nil && st != *o.stats {
			t.Fatalf("op %d: stats got %+v want %+v", i, st, *o.stats)
		}
	}
}

func req(lo, hi int64) plan.Request { return plan.Request{Lo: lo, Hi: hi} }

func sp(hi, lo, f, rcvd, lost, missing, dup, late int64) *ingest.Stats {
	return &ingest.Stats{Hi: hi, Lo: lo, F: f, Received: rcvd, Lost: lost, Missing: missing, Dup: dup, Late: late}
}

func TestScenarios(t *testing.T) {
	cases := []struct {
		name   string
		params ingest.Params
		ops    []op
	}{
		{
			name:   "spec example",
			params: ingest.Params{Lm: 4, K: 2, Tq: 30, R: 2},
			ops: []op{
				{kind: "i", a: 1, now: 0},
				{kind: "i", a: 2, now: 0},
				{kind: "i", a: 3, now: 0, stats: sp(3, 1, 3, 3, 0, 0, 0, 0)},
				{kind: "i", a: 10, now: 0, stats: sp(10, 1, 3, 4, 0, 6, 0, 0)},
				{kind: "p", now: 100, budget: 5, reqs: []plan.Request{req(4, 7), req(8, 8)}},
				{kind: "i", a: 5, now: 110},
				{kind: "i", a: 6, now: 110},
				{kind: "p", now: 120, budget: 10, reqs: []plan.Request{req(9, 9)}},
				{kind: "p", now: 130, budget: 10, reqs: []plan.Request{req(4, 4), req(7, 8)}},
				{kind: "p", now: 160, budget: 10, reqs: []plan.Request{req(9, 9)},
					stats: sp(10, 1, 8, 6, 3, 1, 0, 0)},
				{kind: "i", a: 4, now: 170, stats: sp(10, 1, 8, 6, 3, 1, 0, 1)},
				{kind: "h", a: 10, b: 12, now: 180, stats: sp(12, 10, 10, 6, 4, 2, 0, 1)},
				{kind: "p", now: 180, budget: 10, reqs: []plan.Request{req(11, 12)}},
			},
		},
		{
			name:   "timeout at exactly q+Tq",
			params: ingest.Params{Lm: 4, K: 5, Tq: 30, R: 2},
			ops: []op{
				{kind: "i", a: 5, now: 0},
				{kind: "p", now: 100, budget: 10, reqs: []plan.Request{req(1, 4)}},
				{kind: "p", now: 129, budget: 10},
				{kind: "p", now: 130, budget: 10, reqs: []plan.Request{req(1, 4)}},
			},
		},
		{
			name:   "partial arrival leaves separate segments",
			params: ingest.Params{Lm: 4, K: 5, Tq: 10, R: 2},
			ops: append(ingestOps(1, 2, 3, 4, 5, 6, 7, 8, 9),
				op{kind: "i", a: 14, now: 0},
				op{kind: "p", now: 0, budget: 10, reqs: []plan.Request{req(10, 13)}},
				op{kind: "i", a: 11, now: 1},
				op{kind: "p", now: 10, budget: 10, reqs: []plan.Request{req(10, 10), req(12, 13)}},
			),
		},
		{
			name:   "different c never merges",
			params: ingest.Params{Lm: 4, K: 10, Tq: 10, R: 3},
			ops: append(ingestOps(1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19),
				op{kind: "i", a: 24, now: 0},
				op{kind: "p", now: 0, budget: 2, reqs: []plan.Request{req(20, 21)}},
				op{kind: "p", now: 10, budget: 10, reqs: []plan.Request{req(20, 21), req(22, 23)}},
			),
		},
		{
			name:   "budget truncation and K cutoff",
			params: ingest.Params{Lm: 5, K: 2, Tq: 100, R: 2},
			ops: []op{
				{kind: "i", a: 21, now: 0},
				{kind: "p", now: 0, budget: 7, reqs: []plan.Request{req(1, 5), req(6, 7)}},
				{kind: "p", now: 0, budget: 100, reqs: []plan.Request{req(8, 12), req(13, 17)}},
				{kind: "p", now: 0, budget: 100, reqs: []plan.Request{req(18, 20)}},
			},
		},
		{
			name:   "lost only at R-th timeout then late",
			params: ingest.Params{Lm: 2, K: 2, Tq: 10, R: 2},
			ops: []op{
				{kind: "i", a: 4, now: 0},
				{kind: "p", now: 0, budget: 10, reqs: []plan.Request{req(1, 2), req(3, 3)}},
				{kind: "p", now: 10, budget: 10, reqs: []plan.Request{req(1, 2), req(3, 3)}},
				{kind: "p", now: 20, budget: 10, stats: sp(4, 1, 4, 1, 3, 0, 0, 0)},
				{kind: "i", a: 1, now: 21, stats: sp(4, 1, 4, 1, 3, 0, 0, 1)},
			},
		},
		{
			name:   "hello drops in-flight below lo",
			params: ingest.Params{Lm: 4, K: 2, Tq: 1000, R: 2},
			ops: []op{
				{kind: "i", a: 6, now: 0},
				{kind: "p", now: 0, budget: 3, reqs: []plan.Request{req(1, 3)}},
				{kind: "h", a: 2, b: 6, now: 1, stats: sp(6, 2, 1, 1, 1, 4, 0, 0)},
				{kind: "p", now: 1, budget: 10, reqs: []plan.Request{req(4, 5)}},
				{kind: "i", a: 2, now: 2},
				{kind: "i", a: 1, now: 2, stats: sp(6, 2, 2, 2, 1, 3, 0, 1)},
			},
		},
		{
			name:   "hello with empty buffer",
			params: ingest.Params{Lm: 4, K: 2, Tq: 30, R: 2},
			ops: append(ingestOps(1, 2, 3),
				op{kind: "h", a: 4, b: 3, now: 0, stats: sp(3, 4, 3, 3, 0, 0, 0, 0)},
				op{kind: "i", a: 5, now: 1, stats: sp(5, 4, 3, 4, 0, 1, 0, 0)},
				op{kind: "p", now: 1, budget: 1, reqs: []plan.Request{req(4, 4)}},
			),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { runCase(t, tc.params, tc.ops) })
	}
}

// TestPlanExaminedBound checks that Plan examines at most
// len(requests)+timeouts+1 segments.
func TestPlanExaminedBound(t *testing.T) {
	e, err := ingest.New(ingest.Params{Lm: 2, K: 3, Tq: 10, R: 3})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := e.Register("d"); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := e.Ingest("d", 8, 0); err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if _, err := plan.Plan(e, "d", 0, 3); err != nil {
		t.Fatalf("Plan: %v", err)
	}
	reqs, err := plan.Plan(e, "d", 10, 10)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	examined, timeouts := e.PlanExamined("d")
	bound := len(reqs) + timeouts + 1
	t.Logf("reqs=%v examined=%d timeouts=%d 上界=%d 判定依据: examined<=请求数+超时段数+1",
		reqs, examined, timeouts, bound)
	if examined > bound {
		t.Fatalf("examined %d > bound %d", examined, bound)
	}
	// A scan that drains the set examines exactly one extra (empty) probe.
	e2, _ := ingest.New(ingest.Params{Lm: 4, K: 10, Tq: 10, R: 2})
	if err := e2.Register("d"); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := e2.Ingest("d", 3, 0); err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	reqs2, err := plan.Plan(e2, "d", 0, 10)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	examined2, timeouts2 := e2.PlanExamined("d")
	bound2 := len(reqs2) + timeouts2 + 1
	t.Logf("reqs=%v examined=%d timeouts=%d 上界=%d", reqs2, examined2, timeouts2, bound2)
	if examined2 > bound2 {
		t.Fatalf("examined %d > bound %d", examined2, bound2)
	}
}

// --- naive per-sequence-number simulation used as the oracle ----------

const (
	stMissing = iota
	stReceived
	stLost
)

type simSeq struct {
	st       int
	c        int
	inflight bool
	q        int64
}

type simDev struct {
	hi, lo    int64
	s         []simSeq // 1-based; index 0 unused
	dup, late int64
}

type sim struct {
	p      ingest.Params
	maxNow int64
	devs   map[string]*simDev
}

func newSim(p ingest.Params) *sim {
	return &sim{p: p, maxNow: -1, devs: make(map[string]*simDev)}
}

func (s *sim) register(dev string) {
	s.devs[dev] = &simDev{lo: 1, s: make([]simSeq, 1)}
}

func (s *sim) ingest(dev string, seq, now int64) error {
	if seq < 1 || seq > ingest.MaxSeq || now < 0 || now > ingest.MaxNow {
		return ingest.ErrInvalid
	}
	if now < s.maxNow {
		return ingest.ErrClockBack
	}
	d, ok := s.devs[dev]
	if !ok {
		return ingest.ErrNoDevice
	}
	if seq > d.hi {
		for x := d.hi + 1; x < seq; x++ {
			d.s = append(d.s, simSeq{})
		}
		d.s = append(d.s, simSeq{st: stReceived})
		d.hi = seq
	} else {
		e := &d.s[seq]
		switch e.st {
		case stMissing:
			e.st = stReceived
			e.inflight = false
		case stReceived:
			d.dup++
		case stLost:
			d.late++
		}
	}
	s.maxNow = now
	return nil
}

func (s *sim) hello(dev string, lo2, hi2, now int64) error {
	if lo2 < 1 || lo2 > hi2+1 || hi2 < 0 || hi2 > ingest.MaxSeq || now < 0 || now > ingest.MaxNow {
		return ingest.ErrInvalid
	}
	if now < s.maxNow {
		return ingest.ErrClockBack
	}
	d, ok := s.devs[dev]
	if !ok {
		return ingest.ErrNoDevice
	}
	if lo2 < d.lo || hi2 < d.hi {
		return ingest.ErrRegress
	}
	for x := d.hi + 1; x <= hi2; x++ {
		d.s = append(d.s, simSeq{})
	}
	d.hi = hi2
	for x := int64(1); x < lo2 && x <= d.hi; x++ {
		if d.s[x].st == stMissing {
			d.s[x].st = stLost
			d.s[x].inflight = false
		}
	}
	d.lo = lo2
	s.maxNow = now
	return nil
}

func (s *sim) plan(t *testing.T, dev string, now int64, budget int) ([]plan.Request, error) {
	if budget < 1 || budget > plan.MaxBudget || now < 0 || now > ingest.MaxNow {
		return nil, ingest.ErrInvalid
	}
	if now < s.maxNow {
		return nil, ingest.ErrClockBack
	}
	d, ok := s.devs[dev]
	if !ok {
		return nil, ingest.ErrNoDevice
	}
	for x := int64(1); x <= d.hi; x++ {
		e := &d.s[x]
		if e.st == stMissing && e.inflight && now >= e.q+s.p.Tq {
			e.inflight = false
			if e.c >= s.p.R {
				e.st = stLost
			}
		}
	}
	var reqs []plan.Request
	remaining := int64(budget)
	seq := int64(1)
	requestable := func(x int64) bool { return d.s[x].st == stMissing && !d.s[x].inflight }
	for len(reqs) < s.p.K && remaining > 0 {
		for seq <= d.hi && !requestable(seq) {
			seq++
		}
		if seq > d.hi {
			break
		}
		a := seq
		c := d.s[a].c
		b := a
		for b+1 <= d.hi && requestable(b+1) && d.s[b+1].c == c {
			b++
		}
		n := int64(s.p.Lm)
		if b-a+1 < n {
			n = b - a + 1
		}
		if remaining < n {
			n = remaining
		}
		for x := a; x < a+n; x++ {
			e := &d.s[x]
			e.c++
			if e.c > s.p.R {
				t.Fatalf("seq %d requested %d times > R=%d", x, e.c, s.p.R)
			}
			e.inflight = true
			e.q = now
		}
		reqs = append(reqs, plan.Request{Lo: a, Hi: a + n - 1})
		remaining -= n
		seq = a + n
	}
	s.maxNow = now
	return reqs, nil
}

func (d *simDev) stats() ingest.Stats {
	st := ingest.Stats{Hi: d.hi, Lo: d.lo, Dup: d.dup, Late: d.late}
	f := int64(0)
	for x := int64(1); x <= d.hi; x++ {
		switch d.s[x].st {
		case stReceived:
			st.Received++
		case stLost:
			st.Lost++
		default:
			st.Missing++
		}
	}
	for f+1 <= d.hi && d.s[f+1].st != stMissing {
		f++
	}
	st.F = f
	return st
}

// TestRandomReplay drives 1500 random operation sequences through two
// independent engine instances and the naive per-sequence simulation,
// comparing errors, request lists and stats after every operation.
func TestRandomReplay(t *testing.T) {
	rng := rand.New(rand.NewSource(20261005))
	for trial := 0; trial < 1500; trial++ {
		p := ingest.Params{
			Lm: 1 + rng.Intn(6),
			K:  1 + rng.Intn(4),
			Tq: int64(1 + rng.Intn(30)),
			R:  1 + rng.Intn(3),
		}
		e1, err := ingest.New(p)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		e2, err := ingest.New(p)
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		sm := newSim(p)
		names := []string{"a", "b", "c", "ghost"}
		ndev := 1 + rng.Intn(3)
		for _, d := range names[:ndev] {
			for _, e := range []*ingest.Engine{e1, e2} {
				if err := e.Register(d); err != nil {
					t.Fatalf("Register: %v", err)
				}
			}
			sm.register(d)
		}
		now := int64(0)
		prevF := map[string]int64{}
		prevLost := map[string]int64{}
		ops := 40 + rng.Intn(40)
		for i := 0; i < ops; i++ {
			now += int64(rng.Intn(3))
			if rng.Intn(60) == 0 {
				now-- // clock-back attempt
			}
			dev := names[rng.Intn(len(names))]
			sd, known := sm.devs[dev]
			var gotErr1, gotErr2, wantErr error
			var gotReqs1, gotReqs2, wantReqs []plan.Request
			var desc string
			switch r := rng.Intn(100); {
			case r < 55:
				var seq int64
				if !known {
					seq = 1 + int64(rng.Intn(20))
				} else {
					seq = sd.hi - 8 + rng.Int63n(18)
					if seq < 1 {
						seq = 1
					}
				}
				if rng.Intn(40) == 0 {
					seq = []int64{0, ingest.MaxSeq + 1}[rng.Intn(2)]
				}
				desc = "ingest"
				gotErr1 = e1.Ingest(dev, seq, now)
				gotErr2 = e2.Ingest(dev, seq, now)
				wantErr = sm.ingest(dev, seq, now)
				t.Logf("trial=%d op=%d 输入 ingest(dev=%s,seq=%d,now=%d) 输出 err1=%v err2=%v sim=%v",
					trial, i, dev, seq, now, gotErr1, gotErr2, wantErr)
			case r < 80:
				budget := 1 + rng.Intn(12)
				if rng.Intn(40) == 0 {
					budget = []int{0, plan.MaxBudget + 1}[rng.Intn(2)]
				}
				desc = "plan"
				gotReqs1, gotErr1 = plan.Plan(e1, dev, now, budget)
				gotReqs2, gotErr2 = plan.Plan(e2, dev, now, budget)
				wantReqs, wantErr = sm.plan(t, dev, now, budget)
				t.Logf("trial=%d op=%d 输入 plan(dev=%s,now=%d,budget=%d) 输出 reqs1=%v reqs2=%v sim=%v err=%v/%v/%v",
					trial, i, dev, now, budget, gotReqs1, gotReqs2, wantReqs, gotErr1, gotErr2, wantErr)
			default:
				var lo2, hi2 int64
				if !known {
					lo2 = int64(rng.Intn(5))
					hi2 = int64(rng.Intn(5)) - 1
				} else {
					lo2 = sd.lo - 1 + rng.Int63n(sd.hi-sd.lo+4)
					hi2 = sd.hi - 2 + rng.Int63n(6)
				}
				desc = "hello"
				gotErr1 = e1.Hello(dev, lo2, hi2, now)
				gotErr2 = e2.Hello(dev, lo2, hi2, now)
				wantErr = sm.hello(dev, lo2, hi2, now)
				t.Logf("trial=%d op=%d 输入 hello(dev=%s,lo2=%d,hi2=%d,now=%d) 输出 err1=%v err2=%v sim=%v",
					trial, i, dev, lo2, hi2, now, gotErr1, gotErr2, wantErr)
			}
			if gotErr1 != wantErr || gotErr2 != wantErr {
				t.Fatalf("trial=%d op=%d %s: err1=%v err2=%v sim=%v", trial, i, desc, gotErr1, gotErr2, wantErr)
			}
			if desc == "plan" {
				if !reflect.DeepEqual(gotReqs1, wantReqs) || !reflect.DeepEqual(gotReqs2, wantReqs) {
					t.Fatalf("trial=%d op=%d: reqs1=%v reqs2=%v sim=%v", trial, i, gotReqs1, gotReqs2, wantReqs)
				}
			}
			if !known {
				continue
			}
			st1, err := e1.Stats(dev)
			if err != nil {
				t.Fatalf("trial=%d op=%d stats: %v", trial, i, err)
			}
			st2, _ := e2.Stats(dev)
			want := sd.stats()
			if st1 != want || st2 != want {
				t.Fatalf("trial=%d op=%d %s: stats1=%+v stats2=%+v sim=%+v", trial, i, desc, st1, st2, want)
			}
			if sum := st1.Received + st1.Lost + st1.Missing; sum != st1.Hi {
				t.Fatalf("trial=%d op=%d: conservation broken: %+v", trial, i, st1)
			}
			if st1.F < prevF[dev] {
				t.Fatalf("trial=%d op=%d: f regressed %d -> %d", trial, i, prevF[dev], st1.F)
			}
			if st1.Lost < prevLost[dev] {
				t.Fatalf("trial=%d op=%d: lost regressed %d -> %d", trial, i, prevLost[dev], st1.Lost)
			}
			prevF[dev] = st1.F
			prevLost[dev] = st1.Lost
		}
		if trial%300 == 0 {
			st, _ := e1.Stats(names[0])
			t.Logf("trial=%d 判定依据: 双引擎与朴素模拟逐操作比对 err/reqs/stats 一致, 守恒式与单调性成立; dev=%s stats=%+v",
				trial, names[0], st)
		}
	}
}

// TestConcurrent hammers one device from many goroutines; the result must
// be equivalent to some serial order, so the invariants must hold.
func TestConcurrent(t *testing.T) {
	e, err := ingest.New(ingest.Params{Lm: 4, K: 4, Tq: 5, R: 2})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := e.Register("d"); err != nil {
		t.Fatalf("Register: %v", err)
	}
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 250; i++ {
				_ = e.Ingest("d", int64(g*250+i+1), int64(i%7))
			}
		}(g)
	}
	for g := 0; g < 2; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				_, _ = plan.Plan(e, "d", int64(i), 20)
				_, _ = e.Stats("d")
			}
		}()
	}
	wg.Wait()
	st, err := e.Stats("d")
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if sum := st.Received + st.Lost + st.Missing; sum != st.Hi {
		t.Fatalf("conservation broken: %+v", st)
	}
	if st.Received > 2000 {
		t.Fatalf("received exceeds ingested count: %+v", st)
	}
	t.Logf("并发后 stats=%+v 判定依据: 守恒式 Received+Lost+Missing==Hi", st)
}
