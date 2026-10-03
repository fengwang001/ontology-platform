// Package swap implements a swap-area slot ledger: it allocates slots for
// swapped-out pages, tracks per-slot reference counts and swap-cache flags,
// and recycles slots only when both reach zero. Allocation is cluster-first
// with a next-fit cursor and a FIFO queue of completely free clusters.
package swap

import (
	"errors"
	"sync"
)

var (
	ErrConfig    = errors.New("swap: invalid configuration")
	ErrNoSpace   = errors.New("swap: no free slot")
	ErrRange     = errors.New("swap: slot out of range")
	ErrNotInUse  = errors.New("swap: slot not in use")
	ErrOverflow  = errors.New("swap: reference count overflow")
	ErrUnderflow = errors.New("swap: reference count underflow")
	ErrExists    = errors.New("swap: cache flag already set")
	ErrNoCache   = errors.New("swap: cache flag not set")
)

const (
	maxN   = 1_000_000
	maxK   = 1024
	maxMax = 255
)

// Ledger is a swap-area slot ledger. All methods are safe for concurrent
// use; the result is equivalent to some serial order of the calls.
type Ledger struct {
	mu sync.Mutex

	n   int
	k   int
	max int

	count []uint8
	cache []bool
	used  int

	cur    int // current cluster, -1 when none
	cursor int // next-fit cursor inside cur

	numClusters int
	clusterFree []int
	clusterSize []int

	tree []int // 1-based segment tree over clusterFree
	base int   // index of the first leaf in tree

	queue intQueue

	probes   int64 // non-exported complexity counter
	lastPath int   // which Alloc step produced the last allocation (test aid)
}

// NewLedger builds a ledger with n slots (0..n-1, slot 0 is the swap header
// and never allocatable), cluster size k and per-slot count limit maxCount.
// Out-of-range parameters are rejected as a whole with ErrConfig.
func NewLedger(n, k, maxCount int) (*Ledger, error) {
	if n < 2 || n > maxN || k < 1 || k > maxK || maxCount < 1 || maxCount > maxMax {
		return nil, ErrConfig
	}
	l := &Ledger{n: n, k: k, max: maxCount, cur: -1}
	l.count = make([]uint8, n)
	l.cache = make([]bool, n)

	l.numClusters = (n-1)/k + 1
	l.clusterFree = make([]int, l.numClusters)
	l.clusterSize = make([]int, l.numClusters)
	for c := 0; c < l.numClusters; c++ {
		size := l.clusterEnd(c) - l.clusterStart(c)
		l.clusterSize[c] = size
		l.clusterFree[c] = size
		if size > 0 {
			l.queue.push(c)
		}
	}

	l.base = 1
	for l.base < l.numClusters {
		l.base <<= 1
	}
	l.tree = make([]int, 2*l.base)
	for c := 0; c < l.numClusters; c++ {
		l.tree[l.base+c] = l.clusterFree[c]
	}
	for i := l.base - 1; i >= 1; i-- {
		l.tree[i] = l.tree[2*i] + l.tree[2*i+1]
	}
	return l, nil
}

// clusterStart returns the first usable slot of cluster c (slot 0 excluded).
func (l *Ledger) clusterStart(c int) int {
	if s := c * l.k; s > 1 {
		return s
	}
	return 1
}

// clusterEnd returns the exclusive end slot of cluster c.
func (l *Ledger) clusterEnd(c int) int {
	if e := (c + 1) * l.k; e < l.n {
		return e
	}
	return l.n
}

func (l *Ledger) isFree(s int) bool {
	return l.count[s] == 0 && !l.cache[s]
}

// treeAdd applies delta to the free-slot count of cluster c in the tree.
func (l *Ledger) treeAdd(c, delta int) {
	i := l.base + c
	l.tree[i] += delta
	for i > 1 {
		i >>= 1
		l.tree[i] = l.tree[2*i] + l.tree[2*i+1]
	}
}

// firstFreeCluster returns the lowest-numbered cluster holding at least one
// free slot, or -1 when the pool is full. It examines at most
// 2*ceil(log2(numClusters))+1 tree nodes.
func (l *Ledger) firstFreeCluster() int {
	l.probes++
	if l.tree[1] == 0 {
		return -1
	}
	i := 1
	for i < l.base {
		l.probes += 2
		if l.tree[2*i] > 0 {
			i = 2 * i
		} else {
			i = 2*i + 1
		}
	}
	return i - l.base
}

// slotFreed handles the transition of slot s to the free state.
func (l *Ledger) slotFreed(s int) {
	c := s / l.k
	l.clusterFree[c]++
	l.treeAdd(c, 1)
	l.used--
	if l.clusterSize[c] > 0 && l.clusterFree[c] == l.clusterSize[c] && c != l.cur {
		l.queue.push(c)
	}
}

// slotTaken handles the transition of slot s from free to in-use.
func (l *Ledger) slotTaken(s int) {
	c := s / l.k
	l.clusterFree[c]--
	l.treeAdd(c, -1)
	l.used++
	l.cache[s] = true
}

// Alloc allocates one slot following the three-step order described in the
// package documentation. The returned slot has count 0 and cache set.
func (l *Ledger) Alloc() (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.used == l.n-1 {
		return 0, ErrNoSpace
	}

	// Step 1: next-fit inside the current cluster.
	if l.cur >= 0 {
		start := l.cursor
		if cs := l.clusterStart(l.cur); start < cs {
			start = cs
		}
		end := l.clusterEnd(l.cur)
		for s := start; s < end; s++ {
			l.probes++
			if l.isFree(s) {
				l.slotTaken(s)
				l.cursor = s + 1
				l.lastPath = 1
				return s, nil
			}
		}
	}

	// Step 2: retire cur (enqueue it if it is now completely free) and
	// adopt the head of the free-cluster queue.
	if l.cur >= 0 && l.clusterSize[l.cur] > 0 && l.clusterFree[l.cur] == l.clusterSize[l.cur] {
		l.queue.push(l.cur)
	}
	l.cur = -1
	if c, ok := l.queue.pop(); ok {
		l.cur = c
		s := l.clusterStart(c)
		l.slotTaken(s)
		l.cursor = s + 1
		l.lastPath = 2
		return s, nil
	}

	// Step 3: queue is empty; take the globally lowest free slot.
	c := l.firstFreeCluster()
	if c < 0 {
		return 0, ErrNoSpace
	}
	l.cur = c
	end := l.clusterEnd(c)
	for s := l.clusterStart(c); s < end; s++ {
		l.probes++
		if l.isFree(s) {
			l.slotTaken(s)
			l.cursor = s + 1
			l.lastPath = 3
			return s, nil
		}
	}
	// firstFreeCluster guarantees a free slot in cluster c.
	return 0, ErrNoSpace
}

// Dup increments the reference count of slot s (e.g. fork of a swap entry).
func (l *Ledger) Dup(s int) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.checkDup(s, 0); err != nil {
		return err
	}
	l.count[s]++
	return nil
}

// checkDup validates a Dup on slot s with pending already-simulated extra
// references pending, following the ErrRange, ErrNotInUse, ErrOverflow order.
func (l *Ledger) checkDup(s, pending int) error {
	if s < 1 || s >= l.n {
		return ErrRange
	}
	if l.isFree(s) {
		return ErrNotInUse
	}
	if int(l.count[s])+pending >= l.max {
		return ErrOverflow
	}
	return nil
}

// Free decrements the reference count of slot s.
func (l *Ledger) Free(s int) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.checkFree(s, 0); err != nil {
		return err
	}
	l.count[s]--
	if l.count[s] == 0 && !l.cache[s] {
		l.slotFreed(s)
	}
	return nil
}

// checkFree validates a Free on slot s with pending already-simulated
// releases, following the ErrRange, ErrNotInUse, ErrUnderflow order.
func (l *Ledger) checkFree(s int, pending int) error {
	if s < 1 || s >= l.n {
		return ErrRange
	}
	eff := int(l.count[s]) - pending
	if eff <= 0 && !l.cache[s] {
		return ErrNotInUse
	}
	if eff <= 0 {
		return ErrUnderflow
	}
	return nil
}

// CacheAdd sets the swap-cache flag of slot s.
func (l *Ledger) CacheAdd(s int) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if s < 1 || s >= l.n {
		return ErrRange
	}
	if l.isFree(s) {
		return ErrNotInUse
	}
	if l.cache[s] {
		return ErrExists
	}
	l.cache[s] = true
	return nil
}

// CacheDrop clears the swap-cache flag of slot s.
func (l *Ledger) CacheDrop(s int) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if s < 1 || s >= l.n {
		return ErrRange
	}
	if l.isFree(s) {
		return ErrNotInUse
	}
	if !l.cache[s] {
		return ErrNoCache
	}
	l.cache[s] = false
	if l.count[s] == 0 {
		l.slotFreed(s)
	}
	return nil
}

// Fork applies Dup to each slot in order, atomically: on the first failing
// element it returns (index, error) and the whole batch has no effect.
func (l *Ledger) Fork(slots []int) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	pending := make(map[int]int, len(slots))
	for i, s := range slots {
		if err := l.checkDup(s, pending[s]); err != nil {
			return i, err
		}
		pending[s]++
	}
	for _, s := range slots {
		l.count[s]++
	}
	return -1, nil
}

// Release applies Free to each slot in order, atomically, like Fork.
func (l *Ledger) Release(slots []int) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	pending := make(map[int]int, len(slots))
	for i, s := range slots {
		if err := l.checkFree(s, pending[s]); err != nil {
			return i, err
		}
		pending[s]++
	}
	for _, s := range slots {
		l.count[s]--
		if l.count[s] == 0 && !l.cache[s] {
			l.slotFreed(s)
		}
	}
	return -1, nil
}

// Info returns the reference count and cache flag of slot s.
func (l *Ledger) Info(s int) (int, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if s < 0 || s >= l.n {
		return 0, false
	}
	return int(l.count[s]), l.cache[s]
}

// Used returns the number of in-use slots.
func (l *Ledger) Used() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.used
}

// FreeSlots returns the number of free slots; Used()+FreeSlots() == n-1.
func (l *Ledger) FreeSlots() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.n - 1 - l.used
}

// FreeClusters returns a FIFO snapshot of the completely-free cluster queue.
func (l *Ledger) FreeClusters() []int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.queue.snapshot()
}

// Current returns the current cluster (-1 when none) and the cursor.
func (l *Ledger) Current() (int, int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.cur, l.cursor
}

// intQueue is a FIFO queue of cluster ids with amortized O(1) push/pop.
type intQueue struct {
	buf  []int
	head int
	size int
}

func (q *intQueue) push(v int) {
	if q.size == len(q.buf) {
		n := 2 * len(q.buf)
		if n < 4 {
			n = 4
		}
		nb := make([]int, n)
		for i := 0; i < q.size; i++ {
			nb[i] = q.buf[(q.head+i)%len(q.buf)]
		}
		q.buf = nb
		q.head = 0
	}
	q.buf[(q.head+q.size)%len(q.buf)] = v
	q.size++
}

func (q *intQueue) pop() (int, bool) {
	if q.size == 0 {
		return 0, false
	}
	v := q.buf[q.head]
	q.head = (q.head + 1) % len(q.buf)
	q.size--
	return v, true
}

func (q *intQueue) snapshot() []int {
	out := make([]int, q.size)
	for i := 0; i < q.size; i++ {
		out[i] = q.buf[(q.head+i)%len(q.buf)]
	}
	return out
}
