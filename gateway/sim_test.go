package gateway_test

// 朴素模拟：按规则逐条扫描、用 math/big 计算补充量，与优化实现相互独立，
// 用于随机操作序列的对照测试。

import (
	"math/big"
	"sort"

	"ontology/gateway"
)

type simRec struct {
	seq    uint64
	tenant string
	prio   int
	size   int64
}

type simTenant struct {
	rate, burst        int64
	maxPrio            int
	tokens, lastRefill int64
}

func (t *simTenant) cap() int64 { return t.burst * 1000 }

func (t *simTenant) balanceAt(now int64) int64 {
	bal := big.NewInt(t.tokens)
	if now > t.lastRefill {
		add := big.NewInt(t.rate)
		bal.Add(bal, add.Mul(add, big.NewInt(now-t.lastRefill)))
	}
	if c := big.NewInt(t.cap()); bal.Cmp(c) > 0 {
		bal.Set(c)
	}
	return bal.Int64()
}

func (t *simTenant) settle(now int64) {
	t.tokens = t.balanceAt(now)
	t.lastRefill = now
}

func (t *simTenant) refund(now, size int64) {
	t.settle(now)
	t.tokens += size * 1000
	if t.tokens > t.cap() {
		t.tokens = t.cap()
	}
}

type sim struct {
	cap     int64
	maxNow  int64
	seq     uint64
	tenants map[string]*simTenant
	q       []simRec
}

func newSim(capBytes int64) *sim {
	return &sim{cap: capBytes, maxNow: -1, tenants: map[string]*simTenant{}}
}

func (s *sim) used() (u int64) {
	for _, r := range s.q {
		u += r.size
	}
	return u
}

func (s *sim) ingest(now int64, tenant string, prio int, size int64) ([]simRec, error) {
	if prio < 0 || prio > 2 || size < 1 || size > s.cap || now < 0 || now > 1_000_000_000_000 {
		return nil, gateway.ErrInvalidParam
	}
	if now < s.maxNow {
		return nil, gateway.ErrClockBackward
	}
	tn := s.tenants[tenant]
	if tn == nil {
		return nil, gateway.ErrUnknownTenant
	}
	if prio < tn.maxPrio {
		return nil, gateway.ErrPrioNotAllowed
	}
	if tn.balanceAt(now) < size*1000 {
		return nil, gateway.ErrQuotaExceeded
	}
	need := size - (s.cap - s.used())
	if need > 0 {
		var ev int64
		for _, r := range s.q {
			if r.prio > prio {
				ev += r.size
			}
		}
		if ev < need {
			return nil, gateway.ErrQueueFull
		}
	}
	var evicted []simRec
	if need > 0 {
		var cand []int
		for i, r := range s.q {
			if r.prio > prio {
				cand = append(cand, i)
			}
		}
		sort.Slice(cand, func(a, b int) bool {
			ra, rb := s.q[cand[a]], s.q[cand[b]]
			if ra.prio != rb.prio {
				return ra.prio > rb.prio
			}
			return ra.seq > rb.seq
		})
		var freed int64
		drop := map[int]bool{}
		for _, i := range cand {
			if freed >= need {
				break
			}
			freed += s.q[i].size
			drop[i] = true
			evicted = append(evicted, s.q[i])
		}
		var nq []simRec
		for i, r := range s.q {
			if !drop[i] {
				nq = append(nq, r)
			}
		}
		s.q = nq
		for _, r := range evicted {
			s.tenants[r.tenant].refund(now, r.size)
		}
	}
	tn.settle(now)
	tn.tokens -= size * 1000
	s.seq++
	s.q = append(s.q, simRec{s.seq, tenant, prio, size})
	s.maxNow = now
	return evicted, nil
}

func (s *sim) drain(now, budget int64) ([]simRec, error) {
	if budget < 0 || budget > 1_000_000_000_000 || now < 0 || now > 1_000_000_000_000 {
		return nil, gateway.ErrInvalidParam
	}
	if now < s.maxNow {
		return nil, gateway.ErrClockBackward
	}
	idx := make([]int, len(s.q))
	for i := range idx {
		idx[i] = i
	}
	sort.Slice(idx, func(a, b int) bool {
		ra, rb := s.q[idx[a]], s.q[idx[b]]
		if ra.prio != rb.prio {
			return ra.prio < rb.prio
		}
		return ra.seq < rb.seq
	})
	var out []simRec
	rem := budget
	taken := map[int]bool{}
	for _, i := range idx {
		if s.q[i].size > rem {
			break
		}
		rem -= s.q[i].size
		taken[i] = true
		out = append(out, s.q[i])
	}
	var nq []simRec
	for i, r := range s.q {
		if !taken[i] {
			nq = append(nq, r)
		}
	}
	s.q = nq
	s.maxNow = now
	return out, nil
}
