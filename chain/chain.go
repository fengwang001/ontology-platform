// Package chain implements a single-key copy-on-write version chain.
//
// Versions are stored in ascending order (head = newest = last element).
// Visibility lookups binary-search the ordered version index, so the number
// of compared nodes is logarithmic in the chain length rather than linear.
package chain

import (
	"sort"
	"sync/atomic"
)

// Chain is a copy-on-write version chain for one key.
type Chain struct {
	vers   []int64  // ascending version numbers; head (newest) is the last
	vals   []string // values parallel to vers
	probes int64    // unexported: nodes compared in the last Visible; atomic
}

// New returns an empty chain.
func New() *Chain {
	return &Chain{}
}

// Prepend adds a new head node (value, ver). Old nodes are never touched:
// callers supply strictly increasing ver, so the node is simply appended.
func (c *Chain) Prepend(value string, ver int64) {
	c.vers = append(c.vers, ver)
	c.vals = append(c.vals, value)
}

// Visible returns the value of the greatest version <= s; if no such
// version exists it returns found=false. It records how many nodes the
// binary search compared in the unexported probes counter.
func (c *Chain) Visible(s int64) (string, bool) {
	lo, hi, n := 0, len(c.vers), 0
	for lo < hi { // first index whose version is > s
		mid := (lo + hi) / 2
		n++
		if c.vers[mid] <= s {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	atomic.StoreInt64(&c.probes, int64(n))
	if lo == 0 {
		return "", false
	}
	return c.vals[lo-1], true
}

// Versions returns the chain versions from newest (head) to oldest.
func (c *Chain) Versions() []int64 {
	out := make([]int64, len(c.vers))
	for i, v := range c.vers {
		out[len(c.vers)-1-i] = v
	}
	return out
}

// ProbeGrowthSublinear runs the required complexity experiment inside the
// package (so the unexported probe counter never leaves it): for chains of
// 100, 1000 and 10000 versions it reads at the middle and reports whether
// the compared-node count grows strictly sub-linearly. It returns only a
// verdict — never the numeric counts.
func ProbeGrowthSublinear() bool {
	sizes := []int{100, 1000, 10000}
	prevSize, prevProbes := 0, 0
	for _, m := range sizes {
		c := New()
		for i := 1; i <= m; i++ {
			c.Prepend("v", int64(i))
		}
		if _, ok := c.Visible(int64(m / 2)); !ok {
			return false
		}
		p := int(atomic.LoadInt64(&c.probes))
		if prevSize > 0 {
			sizeRatio := float64(m) / float64(prevSize)
			probeRatio := float64(p) / float64(prevProbes)
			if !(probeRatio < sizeRatio) {
				return false
			}
		}
		prevSize, prevProbes = m, p
	}
	return prevProbes > 0
}

// Collect removes non-head versions invisible to every active snapshot.
// active must hold the active snapshot numbers sorted ascending.
// A non-head node with version v and next-newer version v' is reclaimed
// iff there is no active s with v <= s < v'. The head is never reclaimed.
func (c *Chain) Collect(active []int64) int {
	n := len(c.vers)
	if n < 2 {
		return 0
	}
	keep := make([]bool, n)
	keep[n-1] = true
	reclaimed := 0
	for i := 0; i < n-1; i++ {
		v, vp := c.vers[i], c.vers[i+1]
		j := sort.Search(len(active), func(k int) bool { return active[k] >= v })
		if j < len(active) && active[j] < vp { // an active snapshot sees v
			keep[i] = true
		} else {
			reclaimed++
		}
	}
	if reclaimed == 0 {
		return 0
	}
	nv := make([]int64, 0, n-reclaimed)
	wv := make([]string, 0, n-reclaimed)
	for i := range c.vers {
		if keep[i] {
			nv = append(nv, c.vers[i])
			wv = append(wv, c.vals[i])
		}
	}
	c.vers, c.vals = nv, wv
	return reclaimed
}
