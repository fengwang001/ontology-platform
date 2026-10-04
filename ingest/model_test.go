package ingest

import (
	"ontology/plan"
)

// ---- 逐序号朴素参考模型：与实现并行演化，逐步全状态对照 ----

const (
	mMissing = 0
	mRecv    = 1
	mLost    = 2
)

type model struct {
	cfg                Config
	hi, lo, f, lastNow int64
	state              map[int64]int
	dup, late          int64
	cnt                map[int64]int
	inflight           map[int64]int64
}

func newModel(cfg Config) *model {
	return &model{
		cfg:      cfg,
		lo:       1,
		state:    map[int64]int{},
		cnt:      map[int64]int{},
		inflight: map[int64]int64{},
	}
}

func (m *model) advanceF() {
	for m.f+1 <= m.hi {
		if st := m.state[m.f+1]; st == mRecv || st == mLost {
			m.f++
		} else {
			return
		}
	}
}

func (m *model) ingest(seq, now int64) {
	m.lastNow = now
	if seq > m.hi {
		for x := m.hi + 1; x < seq; x++ {
			m.state[x] = mMissing
		}
		m.hi = seq
		m.state[seq] = mRecv
	} else {
		switch m.state[seq] {
		case mLost:
			m.late++
		case mRecv:
			m.dup++
		default:
			m.state[seq] = mRecv
			delete(m.inflight, seq)
		}
	}
	m.advanceF()
}

func (m *model) hello(lo2, hi2, now int64) {
	m.lastNow = now
	for x := m.hi + 1; x <= hi2; x++ {
		m.state[x] = mMissing
	}
	m.hi = hi2
	for x := m.lo; x < lo2; x++ {
		if st, ok := m.state[x]; ok && st == mMissing {
			m.state[x] = mLost
			delete(m.inflight, x)
		}
	}
	m.lo = lo2
	m.advanceF()
}

func (m *model) settle(now int64) {
	var seqs []int64
	for s, q := range m.inflight {
		if now >= q+m.cfg.Tq {
			seqs = append(seqs, s)
		}
	}
	for _, s := range seqs {
		delete(m.inflight, s)
		if m.cnt[s] >= m.cfg.R {
			m.state[s] = mLost
		}
	}
	m.advanceF()
}

type mseg struct{ a, b, c int64 }

func (m *model) requestable() []mseg {
	var segs []mseg
	x := int64(1)
	for x <= m.hi {
		if m.state[x] == mMissing {
			if _, on := m.inflight[x]; !on {
				c := int64(m.cnt[x])
				y := x
				for y+1 <= m.hi && m.state[y+1] == mMissing {
					if _, on := m.inflight[y+1]; on {
						break
					}
					if int64(m.cnt[y+1]) != c {
						break
					}
					y++
				}
				segs = append(segs, mseg{x, y, c})
				x = y + 1
				continue
			}
		}
		x++
	}
	return segs
}

func (m *model) plan(now, budget int64) []plan.Range {
	m.lastNow = now
	m.settle(now)
	var out []plan.Range
	used := int64(0)
loop:
	for _, sg := range m.requestable() {
		a := sg.a
		for a <= sg.b {
			if used >= budget || len(out) >= m.cfg.K {
				break loop
			}
			cnt := sg.b - a + 1
			if cnt > m.cfg.Lm {
				cnt = m.cfg.Lm
			}
			if cnt > budget-used {
				cnt = budget - used
			}
			b := a + cnt - 1
			out = append(out, plan.Range{A: a, B: b})
			for s := a; s <= b; s++ {
				m.cnt[s]++
				m.inflight[s] = now
			}
			used += cnt
			a = b + 1
		}
	}
	return norm(out)
}

func norm(in []plan.Range) []plan.Range {
	if len(in) == 0 {
		return []plan.Range{}
	}
	return in
}

func (m *model) stats() Stats {
	var rc, lc, mc int64
	for x := int64(1); x <= m.hi; x++ {
		switch m.state[x] {
		case mRecv:
			rc++
		case mLost:
			lc++
		default:
			mc++
		}
	}
	return Stats{Hi: m.hi, Lo: m.lo, F: m.f, Received: rc, Lost: lc, Missing: mc, Dup: m.dup, Late: m.late}
}
