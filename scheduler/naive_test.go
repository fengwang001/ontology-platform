package scheduler_test

// 朴素模拟：每次需要最小 hold 时重扫全部未完成区间与全部批，
// 作为堆实现的对照基准。仅用于测试。

import (
	"math"
	"math/big"
)

type naiveBatch struct {
	id, interval, from, to int64
	acked                  bool
}

type naiveInterval struct {
	id, from, to, next int64
	batches            []*naiveBatch
}

type naive struct {
	intervals map[int64]*naiveInterval
	batches   map[int64]*naiveBatch
	idCount   int64
	batchCnt  int64
	w         int64
}

func newNaive() *naive {
	return &naive{intervals: map[int64]*naiveInterval{}, batches: map[int64]*naiveBatch{}}
}

func (m *naive) done(iv *naiveInterval) bool {
	if iv.next < iv.to {
		return false
	}
	for _, b := range iv.batches {
		if !b.acked {
			return false
		}
	}
	return true
}

// hold 每次重扫：min(next, 最小未确认批起点)。
func (m *naive) hold(iv *naiveInterval) int64 {
	h := iv.next
	for _, b := range iv.batches {
		if !b.acked && b.from < h {
			h = b.from
		}
	}
	return h
}

func (m *naive) advance() {
	minH := int64(math.MaxInt64)
	found := false
	for _, iv := range m.intervals {
		if !m.done(iv) {
			found = true
			if h := m.hold(iv); h < minH {
				minH = h
			}
		}
	}
	if found && minH > m.w {
		m.w = minH
	}
}

type opResult struct {
	kind string
	args []int64
	// 与堆实现的返回顺序保持一致。
	ns []int64
	e  string
	w  int64
}

func errKey(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func (m *naive) add(from, to int64) (int64, string) {
	if from < 0 || to < 0 || to > 1e15 || from >= to {
		return 0, "invalid"
	}
	if from < m.w {
		return 0, "below"
	}
	m.idCount++
	id := m.idCount
	m.intervals[id] = &naiveInterval{id: id, from: from, to: to, next: from}
	m.advance()
	return id, ""
}

func (m *naive) process(id, n int64) (int64, int64, int64, int64, string) {
	if n < 1 || n > 1e6 || id < 1 {
		return 0, 0, 0, 0, "invalid"
	}
	iv := m.intervals[id]
	if iv == nil {
		return 0, 0, 0, 0, "nointerval"
	}
	if iv.next >= iv.to {
		return 0, 0, 0, 0, "exhausted"
	}
	k := iv.to - iv.next
	if n < k {
		k = n
	}
	lo, hi := iv.next, iv.next+k
	iv.next = hi
	m.batchCnt++
	bid := m.batchCnt
	m.batches[bid] = &naiveBatch{id: bid, interval: id, from: lo, to: hi}
	iv.batches = append(iv.batches, m.batches[bid])
	return k, lo, hi, bid, ""
}

func (m *naive) split(id, num, den int64) (int64, string) {
	if num < 0 || den < 1 || den > 1e6 || num > den || id < 1 {
		return 0, "invalid"
	}
	iv := m.intervals[id]
	if iv == nil {
		return 0, "nointerval"
	}
	if iv.next >= iv.to {
		return 0, "exhausted"
	}
	rem := iv.to - iv.next
	var keep int64 = 1
	if num > 0 {
		p := new(big.Int).Mul(big.NewInt(rem), big.NewInt(num))
		p.Add(p, big.NewInt(den-1))
		p.Quo(p, big.NewInt(den))
		if c := p.Int64(); c > keep {
			keep = c
		}
	}
	sp := iv.next + keep
	if sp >= iv.to {
		return 0, "cannotsplit"
	}
	oldTo := iv.to
	iv.to = sp
	m.idCount++
	nid := m.idCount
	m.intervals[nid] = &naiveInterval{id: nid, from: sp, to: oldTo, next: sp}
	m.advance()
	return nid, ""
}

func (m *naive) ack(b int64) string {
	if b < 1 {
		return "invalid"
	}
	bt := m.batches[b]
	if bt == nil {
		return "nobatch"
	}
	if bt.acked {
		return "alreadyacked"
	}
	bt.acked = true
	m.advance()
	return ""
}
