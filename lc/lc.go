// Package lc is the least-connections core: per-server connection counts
// plus a min-heap ordered by (count, index) so Pick locates the
// least-loaded server in O(1) instead of scanning all n servers.
// It depends on no other package and is not concurrency-safe; callers
// must serialize access.
package lc

// Core tracks active connection counts for n servers.
type Core struct {
	counts []int // counts[i] = active connections on server i
	heap   []int // server indices, heap-ordered by (counts[s], s)
	pos    []int // pos[s] = position of server s within heap

	// lastChecked is the number of servers examined to locate the
	// minimum during the most recent Pick. Unexported on purpose: it
	// must never leak through the public API.
	lastChecked int
}

// New builds a Core for n servers, all counts zero. Requires n >= 1.
func New(n int) *Core {
	c := &Core{
		counts: make([]int, n),
		heap:   make([]int, n),
		pos:    make([]int, n),
	}
	for i := range c.heap {
		c.heap[i] = i
		c.pos[i] = i
	}
	return c
}

// less reports whether the server at heap position a orders before the
// one at position b: fewer connections first, ties by smaller index.
func (c *Core) less(a, b int) bool {
	sa, sb := c.heap[a], c.heap[b]
	if c.counts[sa] != c.counts[sb] {
		return c.counts[sa] < c.counts[sb]
	}
	return sa < sb
}

func (c *Core) swap(a, b int) {
	c.heap[a], c.heap[b] = c.heap[b], c.heap[a]
	c.pos[c.heap[a]] = a
	c.pos[c.heap[b]] = b
}

func (c *Core) siftUp(i int) {
	for i > 0 {
		p := (i - 1) / 2
		if !c.less(i, p) {
			return
		}
		c.swap(i, p)
		i = p
	}
}

func (c *Core) siftDown(i int) {
	for {
		l, r := 2*i+1, 2*i+2
		m := i
		if l < len(c.heap) && c.less(l, m) {
			m = l
		}
		if r < len(c.heap) && c.less(r, m) {
			m = r
		}
		if m == i {
			return
		}
		c.swap(i, m)
		i = m
	}
}

// heapify restores heap order after counts were written directly.
func (c *Core) heapify() {
	for i := len(c.heap)/2 - 1; i >= 0; i-- {
		c.siftDown(i)
	}
}

// Pick returns the index of the server with the fewest active
// connections; ties break to the smallest index.
func (c *Core) Pick() int {
	c.lastChecked = 1 // only the heap root is examined
	return c.heap[0]
}

// Incr adds one active connection to server i. Requires a valid i.
func (c *Core) Incr(i int) {
	c.counts[i]++
	c.siftDown(c.pos[i])
}

// Decr removes one active connection from server i. Requires a valid i
// and counts[i] >= 1; the caller (svc) enforces both.
func (c *Core) Decr(i int) {
	c.counts[i]--
	c.siftUp(c.pos[i])
}

// Count reports the active connections on server i.
func (c *Core) Count(i int) int { return c.counts[i] }

// Size reports the number of servers.
func (c *Core) Size() int { return len(c.counts) }

// VerifyPickCost builds cores of increasing size with distinct counts
// and reports whether Pick's examined-server count stays bounded by a
// small constant independent of size. It exposes only pass/fail, never
// the counter's value.
func VerifyPickCost() bool {
	const maxChecked = 2 // heap-root lookup is O(1); a full scan would be m
	for _, m := range []int{100, 1000, 10000} {
		c := New(m)
		for i := range c.counts {
			c.counts[i] = i + 1 // distinct counts
		}
		c.heapify()
		if c.Pick() != 0 || c.lastChecked > maxChecked {
			return false
		}
	}
	return true
}
