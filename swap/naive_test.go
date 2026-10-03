package swap

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// naive is a literal, slow transcription of the specification: linear scans
// over slots and a plain slice for the free-cluster queue. It exists only to
// cross-check Ledger against the rules, step by step.
type naive struct {
	n, k, max int
	count     []int
	cache     []bool
	cur       int
	cursor    int
	queue     []int
}

func newNaive(n, k, maxCount int) *naive {
	m := &naive{n: n, k: k, max: maxCount, cur: -1}
	m.count = make([]int, n)
	m.cache = make([]bool, n)
	for c := 0; c*k < n; c++ {
		if m.clusterLo(c) < m.clusterHi(c) {
			m.queue = append(m.queue, c)
		}
	}
	return m
}

func (m *naive) clusterLo(c int) int {
	if lo := c * m.k; lo > 1 {
		return lo
	}
	return 1
}

func (m *naive) clusterHi(c int) int {
	if hi := (c + 1) * m.k; hi < m.n {
		return hi
	}
	return m.n
}

func (m *naive) free(s int) bool { return m.count[s] == 0 && !m.cache[s] }

func (m *naive) clusterFullyFree(c int) bool {
	lo, hi := m.clusterLo(c), m.clusterHi(c)
	if lo >= hi {
		return false
	}
	for s := lo; s < hi; s++ {
		if !m.free(s) {
			return false
		}
	}
	return true
}

// maybeEnqueue appends the cluster of s if the transition made it completely
// free while it is not the current cluster.
func (m *naive) maybeEnqueue(s int) {
	if c := s / m.k; m.clusterFullyFree(c) && c != m.cur {
		m.queue = append(m.queue, c)
	}
}

func (m *naive) alloc() (int, error) {
	anyFree := false
	for s := 1; s < m.n; s++ {
		if m.free(s) {
			anyFree = true
			break
		}
	}
	if !anyFree {
		return 0, ErrNoSpace
	}
	// Step 1: next-fit inside cur.
	if m.cur >= 0 {
		lo := m.clusterLo(m.cur)
		if m.cursor > lo {
			lo = m.cursor
		}
		for s := lo; s < m.clusterHi(m.cur); s++ {
			if m.free(s) {
				m.cache[s] = true
				m.cursor = s + 1
				return s, nil
			}
		}
	}
	// Step 2: retire cur, adopt the queue head.
	if m.cur >= 0 && m.clusterFullyFree(m.cur) {
		m.queue = append(m.queue, m.cur)
	}
	m.cur = -1
	if len(m.queue) > 0 {
		c := m.queue[0]
		m.queue = m.queue[1:]
		m.cur = c
		s := m.clusterLo(c)
		m.cache[s] = true
		m.cursor = s + 1
		return s, nil
	}
	// Step 3: globally lowest free slot.
	for s := 1; s < m.n; s++ {
		if m.free(s) {
			m.cur = s / m.k
			m.cache[s] = true
			m.cursor = s + 1
			return s, nil
		}
	}
	return 0, ErrNoSpace
}

func (m *naive) dup(s int) error {
	if s < 1 || s >= m.n {
		return ErrRange
	}
	if m.free(s) {
		return ErrNotInUse
	}
	if m.count[s] >= m.max {
		return ErrOverflow
	}
	m.count[s]++
	return nil
}

func (m *naive) freeOne(s int) error {
	if s < 1 || s >= m.n {
		return ErrRange
	}
	if m.free(s) {
		return ErrNotInUse
	}
	if m.count[s] == 0 {
		return ErrUnderflow
	}
	m.count[s]--
	if m.free(s) {
		m.maybeEnqueue(s)
	}
	return nil
}

func (m *naive) cacheAdd(s int) error {
	if s < 1 || s >= m.n {
		return ErrRange
	}
	if m.free(s) {
		return ErrNotInUse
	}
	if m.cache[s] {
		return ErrExists
	}
	m.cache[s] = true
	return nil
}

func (m *naive) cacheDrop(s int) error {
	if s < 1 || s >= m.n {
		return ErrRange
	}
	if m.free(s) {
		return ErrNotInUse
	}
	if !m.cache[s] {
		return ErrNoCache
	}
	m.cache[s] = false
	if m.free(s) {
		m.maybeEnqueue(s)
	}
	return nil
}

func (m *naive) fork(slots []int) (int, error) {
	sim := append([]int(nil), m.count...)
	for i, s := range slots {
		if s < 1 || s >= m.n {
			return i, ErrRange
		}
		if sim[s] == 0 && !m.cache[s] {
			return i, ErrNotInUse
		}
		if sim[s] >= m.max {
			return i, ErrOverflow
		}
		sim[s]++
	}
	copy(m.count, sim)
	return -1, nil
}

func (m *naive) release(slots []int) (int, error) {
	sim := append([]int(nil), m.count...)
	for i, s := range slots {
		if s < 1 || s >= m.n {
			return i, ErrRange
		}
		if sim[s] == 0 && !m.cache[s] {
			return i, ErrNotInUse
		}
		if sim[s] == 0 {
			return i, ErrUnderflow
		}
		sim[s]--
	}
	for _, s := range slots {
		m.count[s]--
		if m.free(s) {
			m.maybeEnqueue(s)
		}
	}
	return -1, nil
}

func (m *naive) used() int {
	u := 0
	for s := 1; s < m.n; s++ {
		if !m.free(s) {
			u++
		}
	}
	return u
}

// checkInvariants verifies the cross-cutting ledger invariants.
func checkInvariants(t *testing.T, l *Ledger) {
	t.Helper()
	used := 0
	sumCounts := 0
	clusterFree := make([]int, l.numClusters)
	for s := 1; s < l.n; s++ {
		if int(l.count[s]) > l.max {
			t.Errorf("slot %d: count %d exceeds Max %d", s, l.count[s], l.max)
		}
		sumCounts += int(l.count[s])
		if l.isFree(s) {
			clusterFree[s/l.k]++
		} else {
			used++
		}
	}
	if used != l.Used() {
		t.Errorf("Used = %d, recomputed %d", l.Used(), used)
	}
	if l.Used()+l.FreeSlots() != l.n-1 {
		t.Errorf("Used+FreeSlots = %d, want %d", l.Used()+l.FreeSlots(), l.n-1)
	}
	for c := 0; c < l.numClusters; c++ {
		if clusterFree[c] != l.clusterFree[c] {
			t.Errorf("cluster %d: clusterFree = %d, recomputed %d", c, l.clusterFree[c], clusterFree[c])
		}
		if l.tree[l.base+c] != l.clusterFree[c] {
			t.Errorf("cluster %d: tree leaf = %d, want %d", c, l.tree[l.base+c], l.clusterFree[c])
		}
	}
	for i := 1; i < l.base; i++ {
		if l.tree[i] != l.tree[2*i]+l.tree[2*i+1] {
			t.Errorf("tree node %d inconsistent", i)
		}
	}
	q := l.FreeClusters()
	seen := make(map[int]bool, len(q))
	for _, c := range q {
		if seen[c] {
			t.Errorf("cluster %d appears twice in queue %v", c, q)
		}
		seen[c] = true
		if c == l.cur {
			t.Errorf("cur cluster %d is in queue %v", c, q)
		}
		if l.clusterSize[c] == 0 || l.clusterFree[c] != l.clusterSize[c] {
			t.Errorf("queued cluster %d is not completely free", c)
		}
	}
	for c := 0; c < l.numClusters; c++ {
		fully := l.clusterSize[c] > 0 && l.clusterFree[c] == l.clusterSize[c]
		if fully && c != l.cur && !seen[c] {
			t.Errorf("completely free cluster %d (not cur) missing from queue %v", c, q)
		}
	}
}

// TestRandomVsNaive replays 2000 random operation sequences against both the
// ledger and the naive model, comparing every result and the full state.
func TestRandomVsNaive(t *testing.T) {
	rng := rand.New(rand.NewSource(20261003))
	for seq := 0; seq < 2000; seq++ {
		n := 2 + rng.Intn(63)
		k := 1 + rng.Intn(8)
		mx := 1 + rng.Intn(3)
		l, err := NewLedger(n, k, mx)
		if err != nil {
			t.Fatal(err)
		}
		m := newNaive(n, k, mx)

		ops := 50 + rng.Intn(150)
		history := make([]string, 0, ops)
		var alive []int
		balance := 0 // successful Dups+Fork elems minus Frees+Release elems

		pickSlot := func() int {
			if len(alive) > 0 && rng.Intn(100) < 70 {
				return alive[rng.Intn(len(alive))]
			}
			return rng.Intn(n + 2) // includes 0 and out-of-range slots
		}
		pickBatch := func() []int {
			b := make([]int, rng.Intn(6))
			for i := range b {
				b[i] = pickSlot()
			}
			return b
		}

		fail := func(format string, args ...any) {
			t.Errorf("seq=%d n=%d k=%d max=%d: %s", seq, n, k, mx, fmt.Sprintf(format, args...))
			t.Errorf("history:\n%s", strings.Join(history, "\n"))
			t.FailNow()
		}

		for i := 0; i < ops; i++ {
			switch rng.Intn(9) {
			case 0, 1: // Alloc
				gs, ge := l.Alloc()
				ms, me := m.alloc()
				history = append(history, fmt.Sprintf("Alloc() -> (%d, %v)", gs, ge))
				if gs != ms || !errors.Is(ge, me) {
					fail("Alloc: ledger=(%d,%v) naive=(%d,%v)", gs, ge, ms, me)
				}
				if ge == nil {
					alive = append(alive, gs)
				}
			case 2: // Dup
				s := pickSlot()
				ge, me := l.Dup(s), m.dup(s)
				history = append(history, fmt.Sprintf("Dup(%d) -> %v", s, ge))
				if !errors.Is(ge, me) {
					fail("Dup(%d): ledger=%v naive=%v", s, ge, me)
				}
				if ge == nil {
					balance++
				}
			case 3: // Free
				s := pickSlot()
				ge, me := l.Free(s), m.freeOne(s)
				history = append(history, fmt.Sprintf("Free(%d) -> %v", s, ge))
				if !errors.Is(ge, me) {
					fail("Free(%d): ledger=%v naive=%v", s, ge, me)
				}
				if ge == nil {
					balance--
				}
			case 4: // CacheAdd
				s := pickSlot()
				ge, me := l.CacheAdd(s), m.cacheAdd(s)
				history = append(history, fmt.Sprintf("CacheAdd(%d) -> %v", s, ge))
				if !errors.Is(ge, me) {
					fail("CacheAdd(%d): ledger=%v naive=%v", s, ge, me)
				}
			case 5: // CacheDrop
				s := pickSlot()
				ge, me := l.CacheDrop(s), m.cacheDrop(s)
				history = append(history, fmt.Sprintf("CacheDrop(%d) -> %v", s, ge))
				if !errors.Is(ge, me) {
					fail("CacheDrop(%d): ledger=%v naive=%v", s, ge, me)
				}
			case 6: // Fork
				b := pickBatch()
				gi, ge := l.Fork(b)
				mi, me := m.fork(b)
				history = append(history, fmt.Sprintf("Fork(%v) -> (%d, %v)", b, gi, ge))
				if gi != mi || !errors.Is(ge, me) {
					fail("Fork(%v): ledger=(%d,%v) naive=(%d,%v)", b, gi, ge, mi, me)
				}
				if ge == nil {
					balance += len(b)
				}
			case 7: // Release
				b := pickBatch()
				gi, ge := l.Release(b)
				mi, me := m.release(b)
				history = append(history, fmt.Sprintf("Release(%v) -> (%d, %v)", b, gi, ge))
				if gi != mi || !errors.Is(ge, me) {
					fail("Release(%v): ledger=(%d,%v) naive=(%d,%v)", b, gi, ge, mi, me)
				}
				if ge == nil {
					balance -= len(b)
				}
			case 8: // queries only
			}

			// Full state comparison after every operation.
			for s := 0; s < n; s++ {
				gc, gch := l.Info(s)
				if gc != m.count[s] || gch != m.cache[s] {
					fail("Info(%d): ledger=(%d,%v) naive=(%d,%v)", s, gc, gch, m.count[s], m.cache[s])
				}
			}
			if l.Used() != m.used() {
				fail("Used: ledger=%d naive=%d", l.Used(), m.used())
			}
			if l.FreeSlots() != n-1-m.used() {
				fail("FreeSlots: ledger=%d naive=%d", l.FreeSlots(), n-1-m.used())
			}
			if !reflect.DeepEqual(l.FreeClusters(), m.queue) {
				fail("FreeClusters: ledger=%v naive=%v", l.FreeClusters(), m.queue)
			}
			gcur, gcursor := l.Current()
			if gcur != m.cur || gcursor != m.cursor {
				fail("Current: ledger=(%d,%d) naive=(%d,%d)", gcur, gcursor, m.cur, m.cursor)
			}
			checkInvariants(t, l)
		}

		sum := 0
		for s := 1; s < n; s++ {
			c, _ := l.Info(s)
			sum += c
		}
		if sum != balance {
			t.Fatalf("seq=%d: sum of counts = %d, dup/free balance = %d", seq, sum, balance)
		}
		t.Logf("seq=%d n=%d k=%d max=%d ops=%d final: used=%d queue=%v cur=%d balance=%d ok",
			seq, n, k, mx, ops, l.Used(), l.FreeClusters(), l.cur, balance)
	}
}

// TestDeterminism: identical operation sequences replay to identical
// allocation sequences and queue states.
func TestDeterminism(t *testing.T) {
	run := func() ([]int, [][]int) {
		l := mustNew(t, 40, 4, 3)
		rng := rand.New(rand.NewSource(7))
		var allocs []int
		var queues [][]int
		for i := 0; i < 500; i++ {
			switch rng.Intn(6) {
			case 0, 1:
				s, err := l.Alloc()
				if err == nil {
					allocs = append(allocs, s)
				}
			case 2:
				l.Dup(rng.Intn(40))
			case 3:
				l.Free(rng.Intn(40))
			case 4:
				l.CacheDrop(rng.Intn(40))
			case 5:
				l.CacheAdd(rng.Intn(40))
			}
			queues = append(queues, l.FreeClusters())
		}
		return allocs, queues
	}
	a1, q1 := run()
	a2, q2 := run()
	if !reflect.DeepEqual(a1, a2) || !reflect.DeepEqual(q1, q2) {
		t.Fatal("identical operation sequences produced different results")
	}
}

// TestConcurrent hammers the ledger from multiple goroutines; the final
// state must still satisfy every invariant (run with -race).
func TestConcurrent(t *testing.T) {
	l := mustNew(t, 4096, 8, 255)
	var dups, frees atomic.Int64
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			for i := 0; i < 3000; i++ {
				s := 1 + rng.Intn(4095)
				switch rng.Intn(8) {
				case 0:
					l.Alloc()
				case 1:
					if l.Dup(s) == nil {
						dups.Add(1)
					}
				case 2:
					if l.Free(s) == nil {
						frees.Add(1)
					}
				case 3:
					l.CacheAdd(s)
				case 4:
					l.CacheDrop(s)
				case 5:
					b := []int{s, rng.Intn(4096)}
					if _, err := l.Fork(b); err == nil {
						dups.Add(int64(len(b)))
					}
				case 6:
					b := []int{s, rng.Intn(4096)}
					if _, err := l.Release(b); err == nil {
						frees.Add(int64(len(b)))
					}
				case 7:
					l.Used()
					l.FreeSlots()
					l.FreeClusters()
					l.Current()
					l.Info(s)
				}
			}
		}(int64(g))
	}
	wg.Wait()
	checkInvariants(t, l)
	sum := 0
	for s := 1; s < 4096; s++ {
		c, _ := l.Info(s)
		sum += c
	}
	if want := dups.Load() - frees.Load(); int64(sum) != want {
		t.Fatalf("sum of counts = %d, dup/free balance = %d", sum, want)
	}
}
