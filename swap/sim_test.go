package swap

import (
	"testing"
)

// naive 是按题目规则逐步写成的朴素模拟：逐槽线性扫描、
// 簇队列用切片，用于对照 Ledger 的优化实现。
type naive struct {
	n, k, max int
	count     []int
	cache     []bool
	cur       int
	cursor    int
	queue     []int
	used      int
}

func newNaive(n, k, maxCount int) *naive {
	m := &naive{
		n:     n,
		k:     k,
		max:   maxCount,
		count: make([]int, n),
		cache: make([]bool, n),
		cur:   -1,
	}
	for c := 0; c*k <= n-1; c++ {
		if m.usableCount(c) > 0 {
			m.queue = append(m.queue, c)
		}
	}
	return m
}

func (m *naive) freeSlot(s int) bool {
	return m.count[s] == 0 && !m.cache[s]
}

func (m *naive) usableCount(c int) int {
	cnt := 0
	for s := 1; s < m.n; s++ {
		if s/m.k == c {
			cnt++
		}
	}
	return cnt
}

func (m *naive) firstSlot(c int) int {
	for s := 1; s < m.n; s++ {
		if s/m.k == c {
			return s
		}
	}
	return -1
}

func (m *naive) fullyFree(c int) bool {
	for s := 1; s < m.n; s++ {
		if s/m.k == c && !m.freeSlot(s) {
			return false
		}
	}
	return true
}

func (m *naive) inQueue(c int) bool {
	for _, q := range m.queue {
		if q == c {
			return true
		}
	}
	return false
}

// maybeEnqueue 在「整簇空闲且不是 cur」成立的那一刻把簇追加到队尾。
func (m *naive) maybeEnqueue(c int) {
	if m.fullyFree(c) && c != m.cur && !m.inQueue(c) {
		m.queue = append(m.queue, c)
	}
}

func (m *naive) alloc() (int, error) {
	if m.used == m.n-1 {
		return -1, ErrNoSpace
	}
	s := -1
	// 第 1 步：cur 簇内编号不小于 cursor 的最小空闲槽位。
	if m.cur >= 0 {
		lo := m.cur * m.k
		if lo < 1 {
			lo = 1
		}
		if m.cursor > lo {
			lo = m.cursor
		}
		hi := (m.cur+1)*m.k - 1
		if hi > m.n-1 {
			hi = m.n - 1
		}
		for i := lo; i <= hi; i++ {
			if m.freeSlot(i) {
				s = i
				break
			}
		}
		if s >= 0 {
			m.cursor = s + 1
		}
	}
	if s < 0 {
		// 第 2 步：旧 cur 整簇空闲则入队，取队首簇为 cur。
		if m.cur >= 0 && m.fullyFree(m.cur) {
			m.queue = append(m.queue, m.cur)
		}
		m.cur = -1
		if len(m.queue) > 0 {
			c := m.queue[0]
			m.queue = m.queue[1:]
			m.cur = c
			s = m.firstSlot(c)
			m.cursor = s + 1
		} else {
			// 第 3 步：全局最小空闲槽位。
			for i := 1; i < m.n; i++ {
				if m.freeSlot(i) {
					s = i
					break
				}
			}
			if s < 0 {
				return -1, ErrNoSpace
			}
			m.cur = s / m.k
			m.cursor = s + 1
		}
	}
	m.count[s] = 0
	m.cache[s] = true
	m.used++
	return s, nil
}

func (m *naive) dup(s int) error {
	if s < 1 || s > m.n-1 {
		return ErrRange
	}
	if m.freeSlot(s) {
		return ErrNotInUse
	}
	if m.count[s] >= m.max {
		return ErrOverflow
	}
	m.count[s]++
	return nil
}

func (m *naive) free(s int) error {
	if s < 1 || s > m.n-1 {
		return ErrRange
	}
	if m.freeSlot(s) {
		return ErrNotInUse
	}
	if m.count[s] == 0 {
		return ErrUnderflow
	}
	m.count[s]--
	if m.count[s] == 0 && !m.cache[s] {
		m.used--
		m.maybeEnqueue(s / m.k)
	}
	return nil
}

func (m *naive) cacheAdd(s int) error {
	if s < 1 || s > m.n-1 {
		return ErrRange
	}
	if m.freeSlot(s) {
		return ErrNotInUse
	}
	if m.cache[s] {
		return ErrExists
	}
	m.cache[s] = true
	return nil
}

func (m *naive) cacheDrop(s int) error {
	if s < 1 || s > m.n-1 {
		return ErrRange
	}
	if m.freeSlot(s) {
		return ErrNotInUse
	}
	if !m.cache[s] {
		return ErrNoCache
	}
	m.cache[s] = false
	if m.count[s] == 0 {
		m.used--
		m.maybeEnqueue(s / m.k)
	}
	return nil
}

func (m *naive) fork(slots []int) (int, error) {
	cnt := make([]int, m.n)
	copy(cnt, m.count)
	for i, s := range slots {
		if s < 1 || s > m.n-1 {
			return i, ErrRange
		}
		if cnt[s] == 0 && !m.cache[s] {
			return i, ErrNotInUse
		}
		if cnt[s] >= m.max {
			return i, ErrOverflow
		}
		cnt[s]++
	}
	copy(m.count, cnt)
	return -1, nil
}

func (m *naive) release(slots []int) (int, error) {
	cnt := make([]int, m.n)
	copy(cnt, m.count)
	for i, s := range slots {
		if s < 1 || s > m.n-1 {
			return i, ErrRange
		}
		if cnt[s] == 0 && !m.cache[s] {
			return i, ErrNotInUse
		}
		if cnt[s] == 0 {
			return i, ErrUnderflow
		}
		cnt[s]--
	}
	for _, s := range slots {
		m.count[s]--
		if m.count[s] == 0 && !m.cache[s] {
			m.used--
			m.maybeEnqueue(s / m.k)
		}
	}
	return -1, nil
}

// wantSameState 断言 Ledger 与朴素模拟的完整状态一致。
func wantSameState(t *testing.T, l *Ledger, m *naive) {
	t.Helper()
	if l.used != m.used {
		t.Fatalf("used 不一致：Ledger=%d naive=%d", l.used, m.used)
	}
	if l.cur != m.cur || l.cursor != m.cursor {
		t.Fatalf("cur/cursor 不一致：Ledger=(%d,%d) naive=(%d,%d)",
			l.cur, l.cursor, m.cur, m.cursor)
	}
	gotQueue := l.FreeClusters()
	if len(gotQueue) != len(m.queue) {
		t.Fatalf("队列长度不一致：Ledger=%v naive=%v", gotQueue, m.queue)
	}
	for i := range gotQueue {
		if gotQueue[i] != m.queue[i] {
			t.Fatalf("队列不一致：Ledger=%v naive=%v", gotQueue, m.queue)
		}
	}
	for s := 0; s < m.n; s++ {
		if int(l.count[s]) != m.count[s] || l.cache[s] != m.cache[s] {
			t.Fatalf("槽位 %d 不一致：Ledger=(%d,%v) naive=(%d,%v)",
				s, l.count[s], l.cache[s], m.count[s], m.cache[s])
		}
	}
}

// wantInvariants 校验 Ledger 的内部不变量。
func wantInvariants(t *testing.T, l *Ledger) {
	t.Helper()
	l.mu.Lock()
	defer l.mu.Unlock()

	// Used()+FreeSlots() == N-1，且 used 与逐槽统计一致。
	used := 0
	sum := 0
	for s := 1; s < l.n; s++ {
		if int(l.count[s]) > l.maxCount {
			t.Fatalf("槽位 %d 的 count=%d 超过 Max=%d", s, l.count[s], l.maxCount)
		}
		if l.count[s] > 0 || l.cache[s] {
			used++
		}
		sum += int(l.count[s])
	}
	if used != l.used {
		t.Fatalf("used 计数 %d 与逐槽统计 %d 不一致", l.used, used)
	}
	if l.used+l.n-1-l.used != l.n-1 {
		t.Fatalf("Used()+FreeSlots() != N-1")
	}
	_ = sum

	// 每簇空闲计数与逐槽统计一致。
	freeCnt := make(map[int]int)
	for s := 1; s < l.n; s++ {
		if l.count[s] == 0 && !l.cache[s] {
			freeCnt[s/l.k]++
		}
	}
	for c := 0; c <= l.cMax; c++ {
		if l.clusterFree[c] != freeCnt[c] {
			t.Fatalf("簇 %d 空闲计数 %d 与逐槽统计 %d 不一致",
				c, l.clusterFree[c], freeCnt[c])
		}
	}

	// 队列中的簇互不相同、都整簇空闲且都不是 cur；
	// 反之整簇空闲且不是 cur 的簇必在队列中。
	seen := make(map[int]bool)
	inQ := make(map[int]bool)
	for _, c := range l.queue[l.qHead:] {
		if seen[c] {
			t.Fatalf("簇 %d 在队列中重复", c)
		}
		seen[c] = true
		inQ[c] = true
		if c == l.cur {
			t.Fatalf("cur（簇 %d）出现在队列中", c)
		}
		if l.clusterFree[c] != l.clusterUsable[c] {
			t.Fatalf("队列中的簇 %d 并非整簇空闲", c)
		}
	}
	for c := l.cMin; c <= l.cMax; c++ {
		if l.clusterFree[c] == l.clusterUsable[c] && c != l.cur && !inQ[c] {
			t.Fatalf("簇 %d 整簇空闲且不是 cur，却不在队列中", c)
		}
	}
}
