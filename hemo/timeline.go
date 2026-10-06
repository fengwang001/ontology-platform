package hemo

// This file implements the per-chair (and per-patient) treatment timeline.
//
// Cost guarantee (see docs/DESIGN.md for the proof):
//
//   The time domain is fixed: timestamps are integers in [0, 10^7]. A
//   4-level radix trie with fan-out 64 (bits 18..23, 12..17, 6..11, 0..5)
//   maps every minute to at most one trie leaf. Finding the predecessor or
//   successor of a given time therefore touches exactly 4 levels; it never
//   scans historical treatments. Feasibility of a chair for a treatment
//   needs only predecessor + successor, so its cost is independent of the
//   total number of historical treatments on the chair or center-wide.
//   Probe counters (ProbeStats) make the bound observable in tests and
//   benchmarks.

import "math/bits"

const trieBits = 64

// trieShift gives the bit shift of level l (0 = top, 3 = leaf).
var trieShift = [4]uint{18, 12, 6, 0}

type trieEntry struct {
	node *trieNode
	list *bucket
}

type trieNode struct {
	present uint64
	child   [trieBits]trieEntry
}

// bucket holds treatments whose Start is in the same 64-minute leaf window.
// Under the no-double-booking invariant starts on one chair timeline never
// collide; a bucket therefore holds at most a small constant number of
// items (1 for committed chair data).
type bucket struct {
	items []*Treatment
}

// Timeline is a set of treatments indexed by Start minute.
type Timeline struct {
	root trieNode
}

// ProbeStats counts internal structural touches for audit benchmarks.
type ProbeStats struct {
	LevelVisits  int
	BitmapScans  int
	ItemsScanned int
}

func indexAt(t int, l uint) int {
	return (t >> trieShift[l]) & (trieBits - 1)
}

// Add inserts t into the timeline.
func (tl *Timeline) Add(t *Treatment) {
	n := &tl.root
	for l := uint(0); l < 3; l++ {
		i := indexAt(t.Start, l)
		n.present |= 1 << uint(i)
		if n.child[i].node == nil {
			n.child[i].node = &trieNode{}
		}
		n = n.child[i].node
	}
	i := indexAt(t.Start, 3)
	n.present |= 1 << uint(i)
	b := n.child[i].list
	if b == nil {
		b = &bucket{}
		n.child[i].list = b
	}
	b.items = append(b.items, t)
}

// Remove deletes the treatment with the same pointer identity as t.
func (tl *Timeline) Remove(t *Treatment) {
	var nodes [3]*trieNode
	var idx [3]int
	n := &tl.root
	for l := uint(0); l < 3; l++ {
		i := indexAt(t.Start, l)
		if n.child[i].node == nil {
			return
		}
		nodes[l] = n
		idx[l] = i
		n = n.child[i].node
	}
	i := indexAt(t.Start, 3)
	b := n.child[i].list
	if b == nil {
		return
	}
	for k, it := range b.items {
		if it == t {
			b.items = append(b.items[:k], b.items[k+1:]...)
			break
		}
	}
	if len(b.items) > 0 {
		return
	}
	n.child[i].list = nil
	n.present &^= 1 << uint(i)
	for l := 2; l >= 0; l-- {
		pn := nodes[l]
		pi := idx[l]
		if pn.child[pi].node.present != 0 {
			break
		}
		pn.child[pi].node = nil
		pn.present &^= 1 << uint(pi)
	}
}

func (b *bucket) maxStartAt(at int, stats *ProbeStats) *Treatment {
	var best *Treatment
	for _, it := range b.items {
		if stats != nil {
			stats.ItemsScanned++
		}
		if it.Start <= at && (best == nil || it.Start > best.Start ||
			(it.Start == best.Start && it.End > best.End)) {
			best = it
		}
	}
	return best
}

func (b *bucket) minStartAt(at int, stats *ProbeStats) *Treatment {
	var best *Treatment
	for _, it := range b.items {
		if stats != nil {
			stats.ItemsScanned++
		}
		if it.Start >= at && (best == nil || it.Start < best.Start) {
			best = it
		}
	}
	return best
}

// Predecessor returns the treatment with the greatest Start <= at (largest
// End breaking ties), or nil when no such treatment exists.
func (tl *Timeline) Predecessor(at int, stats *ProbeStats) *Treatment {
	if at < MinTime {
		return nil
	}
	if at > MaxTime {
		at = MaxTime
	}
	var path [4]int
	for l := uint(0); l < 4; l++ {
		path[l] = indexAt(at, l)
	}
	nodes := [4]*trieNode{&tl.root}
	n := &tl.root
	for l := uint(0); l < 3; l++ {
		i := path[l]
		if stats != nil {
			stats.LevelVisits++
		}
		mask := n.present &^ (^uint64(0) << uint(i+1)) // children <= i
		if mask == 0 {
			return tl.backMax(nodes[:l+1], path[:l+1], stats)
		}
		j := bits.Len64(mask) - 1
		if stats != nil {
			stats.BitmapScans++
		}
		if j != i {
			return tl.maxInSubtree(n.child[j].node, uint(l)+1, stats)
		}
		n = n.child[j].node
		nodes[l+1] = n
	}
	if stats != nil {
		stats.LevelVisits++
	}
	i := path[3]
	mask := n.present &^ (^uint64(0) << uint(i+1))
	if mask != 0 {
		j := bits.Len64(mask) - 1
		if stats != nil {
			stats.BitmapScans++
		}
		if b := n.child[j].list; b != nil {
			if j < i {
				if it := b.maxStartAt(MaxTime, stats); it != nil {
					return it
				}
			} else if it := b.maxStartAt(at, stats); it != nil {
				return it
			}
		}
	}
	return tl.backMax(nodes[:4], path[:4], stats)
}

// backMax walks the recorded root-to-node path upward and returns the
// maximum item in the greatest occupied sibling strictly left of the path.
func (tl *Timeline) backMax(nodes []*trieNode, path []int, stats *ProbeStats) *Treatment {
	for l := len(nodes) - 1; l >= 0; l-- {
		if stats != nil {
			stats.LevelVisits++
		}
		mask := nodes[l].present & ((uint64(1) << uint(path[l])) - 1)
		if mask == 0 {
			continue
		}
		j := bits.Len64(mask) - 1
		if stats != nil {
			stats.BitmapScans++
		}
		e := nodes[l].child[j]
		if e.list != nil {
			return e.list.maxStartAt(MaxTime, stats)
		}
		return tl.maxInSubtree(e.node, uint(l)+1, stats)
	}
	return nil
}

func (tl *Timeline) maxInSubtree(n *trieNode, level uint, stats *ProbeStats) *Treatment {
	for {
		if n == nil || n.present == 0 {
			return nil
		}
		if stats != nil {
			stats.LevelVisits++
		}
		j := bits.Len64(n.present) - 1
		if stats != nil {
			stats.BitmapScans++
		}
		if level == 3 {
			if b := n.child[j].list; b != nil {
				return b.maxStartAt(MaxTime, stats)
			}
			return nil
		}
		n = n.child[j].node
		level++
	}
}

// Successor returns the treatment with the smallest Start >= at (smallest
// End breaking ties), or nil.
func (tl *Timeline) Successor(at int, stats *ProbeStats) *Treatment {
	if at > MaxTime {
		return nil
	}
	if at < MinTime {
		at = MinTime
	}
	var path [4]int
	for l := uint(0); l < 4; l++ {
		path[l] = indexAt(at, l)
	}
	nodes := [4]*trieNode{&tl.root}
	n := &tl.root
	for l := uint(0); l < 3; l++ {
		i := path[l]
		if stats != nil {
			stats.LevelVisits++
		}
		mask := n.present & (^uint64(0) << uint(i)) // children >= i
		if mask == 0 {
			return tl.backMin(nodes[:l+1], path[:l+1], stats)
		}
		j := bits.TrailingZeros64(mask)
		if stats != nil {
			stats.BitmapScans++
		}
		if j != i {
			return tl.minInSubtree(n.child[j].node, uint(l)+1, stats)
		}
		n = n.child[j].node
		nodes[l+1] = n
	}
	if stats != nil {
		stats.LevelVisits++
	}
	i := path[3]
	mask := n.present & (^uint64(0) << uint(i))
	if mask != 0 {
		j := bits.TrailingZeros64(mask)
		if stats != nil {
			stats.BitmapScans++
		}
		if b := n.child[j].list; b != nil {
			if j > i {
				if it := b.minStartAt(MinTime, stats); it != nil {
					return it
				}
			} else if it := b.minStartAt(at, stats); it != nil {
				return it
			}
		}
	}
	return tl.backMin(nodes[:4], path[:4], stats)
}

func (tl *Timeline) backMin(nodes []*trieNode, path []int, stats *ProbeStats) *Treatment {
	for l := len(nodes) - 1; l >= 0; l-- {
		if stats != nil {
			stats.LevelVisits++
		}
		mask := nodes[l].present &^ ((uint64(1) << uint(path[l]+1)) - 1)
		if mask == 0 {
			continue
		}
		j := bits.TrailingZeros64(mask)
		if stats != nil {
			stats.BitmapScans++
		}
		e := nodes[l].child[j]
		if e.list != nil {
			return e.list.minStartAt(MinTime, stats)
		}
		return tl.minInSubtree(e.node, uint(l)+1, stats)
	}
	return nil
}

func (tl *Timeline) minInSubtree(n *trieNode, level uint, stats *ProbeStats) *Treatment {
	for {
		if n == nil || n.present == 0 {
			return nil
		}
		if stats != nil {
			stats.LevelVisits++
		}
		j := bits.TrailingZeros64(n.present)
		if stats != nil {
			stats.BitmapScans++
		}
		if level == 3 {
			if b := n.child[j].list; b != nil {
				return b.minStartAt(MinTime, stats)
			}
			return nil
		}
		n = n.child[j].node
		level++
	}
}
