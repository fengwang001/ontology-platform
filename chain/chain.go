// Package chain implements a single key's copy-on-write version chain.
// New versions are prepended as immutable (value, ver) nodes; old nodes are
// never rewritten. An ascending index supports logarithmic visibility lookup.
package chain

import (
	"sort"
	"sync/atomic"
)

// node is one immutable version on the chain.
type node struct {
	value string
	ver   int64
	next  *node // next-OLDER node (toward the tail); nil for the oldest
}

// Chain is one key's version chain plus a version-ordered index.
type Chain struct {
	head *node        // newest version
	idx  []*node      // ascending by ver; ordered index for binary search
	cmps atomic.Int64 // unexported: nodes compared by the latest Visible call
}

// New returns an empty chain.
func New() *Chain { return &Chain{} }

// Prepend creates a new immutable head node. ver must be greater than every
// existing version; the old head is left untouched (copy-on-write).
func (c *Chain) Prepend(value string, ver int64) {
	n := &node{value: value, ver: ver, next: c.head}
	c.head = n
	c.idx = append(c.idx, n) // versions strictly increase, so append stays ordered
}

// Visible returns the value of the greatest version <= s, or found=false.
// It locates the version by binary search over the ordered index.
func (c *Chain) Visible(s int64) (string, bool) {
	c.cmps.Store(0)
	// First index whose version is strictly greater than s.
	i := sort.Search(len(c.idx), func(j int) bool {
		c.cmps.Add(1)
		return c.idx[j].ver > s
	})
	if i == 0 {
		return "", false
	}
	n := c.idx[i-1]
	return n.value, true
}

// Vers returns versions newest-first (a fresh slice).
func (c *Chain) Vers() []int64 {
	out := []int64{}
	for n := c.head; n != nil; n = n.next {
		out = append(out, n.ver)
	}
	return out
}

// SublinearProbe reports whether mid-chain Visible lookups stay logarithmic
// as the chain grows 100x (100 -> 1000 -> 10000). It exposes only a verdict:
// the unexported comparison count itself never crosses the package boundary.
func SublinearProbe() bool {
	var first int64
	for gi, m := range []int{100, 1000, 10000} {
		c := New()
		for v := 1; v <= m; v++ {
			c.Prepend("v", int64(v))
		}
		if _, ok := c.Visible(int64(m / 2)); !ok {
			return false
		}
		got := c.cmps.Load()
		if got > 20 { // ceil(log2(10000)) ~ 14; a head-down linear scan ~ 5000
			return false
		}
		if gi == 0 {
			first = got
		} else if got > first+8 { // 100x nodes may add at most log2(100) ~ 7 probes
			return false
		}
	}
	return true
}

// vp = +∞ for the head which is never removed) when no active snapshot s
// satisfies v <= s < vp. visibleToActive reports such an s. Returns the count.
func (c *Chain) Collect(visibleToActive func(v, vp int64) bool) int {
	if len(c.idx) == 0 {
		return 0
	}
	keep := make([]*node, 0, len(c.idx))
	// idx is ascending; head is its last element and is always retained.
	for i, n := range c.idx {
		if i == len(c.idx)-1 {
			keep = append(keep, n) // head
			continue
		}
		vp := c.idx[i+1].ver
		if visibleToActive(n.ver, vp) {
			keep = append(keep, n)
		}
	}
	removed := len(c.idx) - len(keep)
	// Relink retained nodes newest.next -> older; node contents stay untouched.
	for i := range keep {
		if i == 0 {
			keep[i].next = nil // oldest retained
		} else {
			keep[i].next = keep[i-1]
		}
	}
	c.head = keep[len(keep)-1]
	c.idx = keep
	return removed
}
