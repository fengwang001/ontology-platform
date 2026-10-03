package approval_test

import (
	"errors"
	"fmt"
	"math/rand"
	"testing"

	"ontology/approval"
)

// 朴素参考模型：逐级逐时刻推演；到期先落实再校验本体，失败整体回滚。错误用单字符码。
type nreq struct {
	amt, ta, final int64
	cand           []string
	idx, out       int // 0待 1准 2拒 3超时
}
type snap struct {
	idx, out  int
	ta, final int64
}
type naive struct {
	lim      map[string]int64
	manager  map[string]string
	T, clock int64
	reqs     map[string]*nreq
}

func newNaive(T int64) *naive {
	return &naive{lim: map[string]int64{}, manager: map[string]string{}, T: T, reqs: map[string]*nreq{}}
}

func (m *naive) chain(a string) []string {
	out, seen, cur := []string{}, map[string]bool{a: true}, m.manager[a]
	for cur != "" && !seen[cur] {
		out, seen[cur], cur = append(out, cur), true, m.manager[cur]
	}
	return out
}
func (m *naive) expireAll(now int64) map[string]snap {
	snaps := map[string]snap{}
	for id, r := range m.reqs {
		for r.out == 0 && r.ta+m.T <= now {
			if _, ok := snaps[id]; !ok {
				snaps[id] = snap{idx: r.idx, out: r.out, ta: r.ta, final: r.final}
			}
			at := r.ta + m.T
			if r.idx+1 < len(r.cand) {
				r.idx, r.ta = r.idx+1, at
			} else {
				r.out, r.final = 3, at
			}
		}
	}
	return snaps
}

func (m *naive) rollback(s map[string]snap) {
	for id, x := range s {
		m.reqs[id].idx, m.reqs[id].ta, m.reqs[id].final, m.reqs[id].out = x.idx, x.ta, x.final, x.out
	}
}

func (m *naive) submit(id, a string, amt, now int64) string {
	if id == "" || a == "" || amt < 1 || amt > 1e12 || now < 0 || now > 1e15 {
		return "I"
	}
	if now < m.clock {
		return "K"
	}
	s := m.expireAll(now)
	if _, ok := m.reqs[id]; ok {
		m.rollback(s)
		return "N"
	}
	cand := []string{}
	for _, p := range m.chain(a) {
		if m.lim[p] >= amt {
			cand = append(cand, p)
		}
	}
	if len(cand) == 0 {
		m.rollback(s)
		return "X"
	}
	m.reqs[id] = &nreq{amt: amt, cand: cand, ta: now}
	m.clock = now
	return ""
}

func (m *naive) decide(id, who string, ok bool, now int64) string {
	if id == "" || who == "" || now < 0 || now > 1e15 {
		return "I"
	}
	if now < m.clock {
		return "K"
	}
	s := m.expireAll(now)
	r, exists := m.reqs[id]
	code := func(c string) string { m.rollback(s); return c }
	switch {
	case !exists:
		return code("N")
	case r.out != 0:
		return code("C")
	case who != r.cand[r.idx]: // ErrNotAssignee 优先于 ErrRevoked
		return code("A")
	case m.lim[who] < r.amt:
		return code("R")
	}
	r.out = 2
	if ok {
		r.out = 1
	}
	r.final, m.clock = now, now
	return ""
}

func (m *naive) view(id string, now int64) (string, int64, int64, int64) {
	r := m.reqs[id]
	if r.out != 0 {
		return "", r.final, int64(r.out), r.final
	}
	idx, t := r.idx, r.ta
	for t+m.T <= now {
		if idx+1 < len(r.cand) {
			idx, t = idx+1, t+m.T
		} else {
			return "", t + m.T, 3, t + m.T
		}
	}
	return r.cand[idx], t, 0, 0
}

var canonMap = map[error]string{
	approval.ErrInvalid: "I", approval.ErrClock: "K", approval.ErrNotFound: "N",
	approval.ErrClosed: "C", approval.ErrNotAssignee: "A",
	approval.ErrRevoked: "R", approval.ErrNoApprover: "X",
}

func canon(err error) string {
	for k, v := range canonMap {
		if errors.Is(err, k) {
			return v
		}
	}
	return ""
}
func TestRandomSimulation(t *testing.T) {
	for seed := int64(0); seed < 150; seed++ {
		rng := rand.New(rand.NewSource(seed))
		people := []string{"A", "B", "C", "D", "E"}
		eng, o := mkEngine(t, 10)
		mdl := newNaive(10)
		for i, p := range people {
			if i+1 < len(people) && rng.Intn(2) == 0 {
				g := people[i+1+rng.Intn(len(people)-i-1)]
				must(t, o.SetManager(p, g))
				mdl.manager[p] = g
			}
			x := int64(rng.Intn(4)) * 100
			must(t, o.SetLimit(p, x))
			mdl.lim[p] = x
		}
		for step := 0; step < 350; step++ {
			now := mdl.clock + int64(rng.Intn(12))
			switch rng.Intn(5) {
			case 0, 1:
				id := fmt.Sprintf("r%d", rng.Intn(10))
				a := people[rng.Intn(5)]
				amt := int64(100 * (1 + rng.Intn(5)))
				w, g := mdl.submit(id, a, amt, now), canon(eng.Submit(id, a, amt, now))
				if w != g {
					t.Fatalf("s=%d/%d sub %s/%s/%d@%d %q!=%q", seed, step, id, a, amt, now, w, g)
				}
			case 2, 3:
				id := fmt.Sprintf("r%d", rng.Intn(10))
				who := people[rng.Intn(5)]
				ok := rng.Intn(2) == 0
				if w, g := mdl.decide(id, who, ok, now), canon(eng.Decide(id, who, ok, now)); w != g {
					t.Fatalf("s=%d/%d dec %s/%s@%d %q!=%q", seed, step, id, who, now, w, g)
				}
			default: // SetLimit 不带时钟，两模型同步，不触发升级
				p, x := people[rng.Intn(5)], int64(rng.Intn(4))*100
				must(t, o.SetLimit(p, x))
				mdl.lim[p] = x
			}
			for id := range mdl.reqs { // Status 只读逐申请对照，不推进时钟
				est, err := eng.Status(id, now)
				if err != nil {
					t.Fatalf("status %s: %v", id, err)
				}
				na, nt, no, nf := mdl.view(id, now)
				if est.Assignee != na || est.Ta != nt || int64(est.Outcome) != no || est.FinalAt != nf {
					t.Fatalf("s=%d/%d %s@%d %+v vs (%s,%d,%d,%d)", seed, step, id, now, est, na, nt, no, nf)
				}
			}
		}
	}
}
