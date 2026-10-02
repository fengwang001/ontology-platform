package leaseread

import (
	"math/rand"
	"sort"
)

// naiveArbiter is a line-by-line transcription of the specification, kept
// deliberately simple so it can cross-check the optimized implementation.
type naiveArbiter struct {
	n, q         int
	dur, rho, et int64
	start        int64
	ack          []int64
	has          []bool
	pr           []int64
	pending      []naiveRead
	leader       bool
	t            int64
	nextId       int64
	lastExamined int64
	stepdownAt   int64
}

type naiveRead struct {
	id int64
	a  int64
}

type opResult struct {
	kind     string // "ack", "read", "tick", "votes"
	errIs    string // "", "config", "arg", "rewind", "leader"
	ids      []int64
	readKind string
	readId   int64
	expire   int64
	votes    int
}

func newNaive(n int, dur, rho, et, start int64) *naiveArbiter {
	return &naiveArbiter{
		n: n, q: n/2 + 1, dur: dur, rho: rho, et: et, start: start,
		ack: make([]int64, n), has: make([]bool, n), pr: make([]int64, n),
		leader: true, t: start, nextId: 1, stepdownAt: -1,
	}
}

func (m *naiveArbiter) leaseLen() int64 { return m.dur * (1000 - m.rho) / 1000 }

func (m *naiveArbiter) leaseBase(now int64) (int64, bool) {
	need := m.q - 1
	if need == 0 {
		return now, true
	}
	var s []int64
	for f := 1; f < m.n; f++ {
		if m.has[f] {
			s = append(s, m.ack[f])
		}
	}
	if len(s) < need {
		return 0, false
	}
	sort.Slice(s, func(i, j int) bool { return s[i] > s[j] })
	return s[need-1], true
}

type op struct {
	kind   byte // 'a' ack, 'r' read, 't' tick, 'v' votes
	i      int
	s, val int64
}

func errName(err error) string {
	switch err {
	case nil:
		return ""
	case ErrInvalidConfig:
		return "config"
	case ErrInvalidArg:
		return "arg"
	case ErrClockRewind:
		return "rewind"
	case ErrNotLeader:
		return "leader"
	}
	return "other"
}

func (m *naiveArbiter) apply(o op) opResult {
	switch o.kind {
	case 'a':
		if o.i < 1 || o.i > m.n-1 || o.s < 0 || o.s > maxTime ||
			o.val < 0 || o.val > maxTime || o.s > o.val {
			return opResult{kind: "ack", errIs: "arg"}
		}
		if o.val < m.t {
			return opResult{kind: "ack", errIs: "rewind"}
		}
		if o.s > m.ack[o.i] {
			m.ack[o.i] = o.s
		}
		m.has[o.i] = true
		if x := o.val + m.dur; x > m.pr[o.i] {
			m.pr[o.i] = x
		}
		m.t = o.val
		res := opResult{kind: "ack", ids: []int64{}}
		m.lastExamined = 0
		if m.leader {
			for len(m.pending) > 0 {
				m.lastExamined++
				rd := m.pending[0]
				c := 0
				for f := 1; f < m.n; f++ {
					if m.has[f] && m.ack[f] >= rd.a {
						c++
					}
				}
				if c >= m.q-1 {
					res.ids = append(res.ids, rd.id)
					m.pending = m.pending[1:]
					continue
				}
				break
			}
		}
		return res
	case 'r':
		now := o.val
		if now < 0 || now > maxTime {
			return opResult{kind: "read", errIs: "arg"}
		}
		if now < m.t {
			return opResult{kind: "read", errIs: "rewind"}
		}
		if !m.leader {
			return opResult{kind: "read", errIs: "leader"}
		}
		m.t = now
		res := opResult{kind: "read"}
		if m.n == 1 {
			res.readKind = "local"
			res.expire = now + m.leaseLen()
			return res
		}
		if base, ok := m.leaseBase(now); ok {
			e := base + m.leaseLen()
			if now < e {
				res.readKind = "local"
				res.expire = e
				return res
			}
		}
		m.pending = append(m.pending, naiveRead{id: m.nextId, a: now})
		res.readKind = "pending"
		res.readId = m.nextId
		m.nextId++
		return res
	case 't':
		now := o.val
		if now < 0 || now > maxTime {
			return opResult{kind: "tick", errIs: "arg"}
		}
		if now < m.t {
			return opResult{kind: "tick", errIs: "rewind"}
		}
		res := opResult{kind: "tick", ids: []int64{}}
		if m.leader && now >= m.start+m.et {
			c := 0
			thr := now - m.et
			for f := 1; f < m.n; f++ {
				if m.has[f] && m.ack[f] >= thr {
					c++
				}
			}
			if c+1 < m.q {
				m.leader = false
				m.stepdownAt = now
				for _, rd := range m.pending {
					res.ids = append(res.ids, rd.id)
				}
				m.pending = nil
			}
		}
		m.t = now
		return res
	case 'v':
		now := o.val
		if now < 0 || now > maxTime {
			return opResult{kind: "votes", errIs: "arg"}
		}
		if now < m.t {
			return opResult{kind: "votes", errIs: "rewind"}
		}
		v := 0
		for f := 1; f < m.n; f++ {
			if now >= m.pr[f] {
				v++
			}
		}
		if !m.leader {
			v++
		}
		return opResult{kind: "votes", votes: v}
	}
	panic("unknown op")
}

func (m *naiveArbiter) localAt(now int64) (bool, int64) {
	if !m.leader {
		return false, 0
	}
	if m.n == 1 {
		return true, now + m.leaseLen()
	}
	base, ok := m.leaseBase(now)
	if !ok {
		return false, 0
	}
	e := base + m.leaseLen()
	return now < e, e
}

// genCase builds one random configuration and operation trace.
func genCase(rng *rand.Rand) (int, int64, int64, int64, int64, []op) {
	n := 1 + rng.Intn(9)
	dur := int64(1 + rng.Intn(50))
	rho := int64(rng.Intn(1000))
	et := dur + int64(rng.Intn(80))
	start := int64(rng.Intn(20))
	opsN := 30 + rng.Intn(60)
	ops := make([]op, 0, opsN)
	var t int64
	for k := 0; k < opsN; k++ {
		roll := rng.Intn(100)
		switch {
		case roll < 40:
			i := 0
			if n > 1 {
				i = 1 + rng.Intn(n-1)
			}
			if i == 0 {
				// N==1: every Ack is invalid by index; still emit some to
				// exercise rejection, but do not move the local clock.
				ops = append(ops, op{kind: 'a', i: 0, s: t, val: t})
				continue
			}
			s := t + int64(rng.Intn(30))
			r := s + int64(rng.Intn(20))
			ops = append(ops, op{kind: 'a', i: i, s: s, val: r})
			t = r
		case roll < 75:
			now := t + int64(rng.Intn(40))
			if rng.Intn(12) == 0 {
				now = t - int64(1+rng.Intn(5)) // rewind attempt
			}
			ops = append(ops, op{kind: 'r', val: now})
			t = max64(t, now)
		case roll < 90:
			now := t + int64(rng.Intn(40))
			if rng.Intn(12) == 0 {
				now = t - 1
			}
			ops = append(ops, op{kind: 't', val: now})
			t = max64(t, now)
		default:
			now := t + int64(rng.Intn(40))
			ops = append(ops, op{kind: 'v', val: now})
			t = max64(t, now)
		}
	}
	return n, dur, rho, et, start, ops
}

func max64(x, y int64) int64 {
	if y > x {
		return y
	}
	return x
}

func resultsEqual(x, y opResult) bool {
	if x.kind != y.kind || x.errIs != y.errIs || x.readKind != y.readKind ||
		x.readId != y.readId || x.expire != y.expire || x.votes != y.votes {
		return false
	}
	return intsEqual(x.ids, y.ids)
}

func intsEqual(x, y []int64) bool {
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
