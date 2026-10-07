package bitemporal

import (
	"math/bits"
	"sync"
)

// The int64 valid-time domain [MinTick, MaxTick) is mapped order-preservingly
// onto the uint64 domain [0, 2^64-1) via u = uint64(t) + 2^63. Interval
// arithmetic is done in uint64 to avoid signed overflow. Note that Go keeps
// shifts like 1<<64 equal to 1 (no truncation), so this file never forms 1<<64
// and instead uses bits.TrailingZeros64 / LeadingZeros64.
const (
	treeDepth  = 64
	domainBias = uint64(1) << 63
)

func toU(t Tick) uint64 { return uint64(t) + domainBias }

// nodeKey identifies one dyadic node. Level L holds aligned blocks of size
// 2^(64-L): level 0 is the conceptual 2^64 root (never materialized), level 1
// holds two 2^63 blocks, and level 64 is the single-point leaf.
type nodeKey struct {
	level int
	index uint64 // u / blockSize
}

// chainEntry is one record appended to a node's chain. Seq strictly increases
// within every chain because Writes are append-only.
type chainEntry struct {
	Seq    uint64
	TxTime Tick
}

// segmentIndex splits every inserted interval into O(64) canonical dyadic
// nodes and keeps one Seq-ordered chain per node. A point query walks the
// 65-node root-to-leaf path and binary-searches each chain, so the number of
// comparisons is independent of the object's total history.
type segmentIndex struct {
	mu        sync.RWMutex
	chains    map[nodeKey][]chainEntry
	inspected int
}

func newSegmentIndex() *segmentIndex {
	return &segmentIndex{chains: map[nodeKey][]chainEntry{}}
}

// canonicalCover partitions [lo, hi) (hi > lo) into dyadic nodes.
func canonicalCover(lo, hi uint64) []nodeKey {
	var keys []nodeKey
	for lo < hi {
		// Largest power-of-two block aligned at lo: 2^v2(lo), capped at 2^63
		// (a 2^64 block cannot be represented) and by the remaining span.
		aligned := uint64(1) << 63
		if lo != 0 {
			if tz := bits.TrailingZeros64(lo); tz < 63 {
				aligned = uint64(1) << tz
			}
		}
		rem := hi - lo // >= 1 because lo < hi
		size := aligned
		if rem < size {
			size = uint64(1) << (63 - bits.LeadingZeros64(rem))
		}
		exp := log2pow2(size)
		level := 64 - exp // size 2^63 -> L1; size 1 -> L64
		keys = append(keys, nodeKey{level: level, index: lo >> exp})
		if lo == ^uint64(0) { // last point; adding 1 would wrap to 0
			break
		}
		lo += size
	}
	return keys
}

// log2pow2 returns log2 of a power-of-two s in [1, 2^63].
func log2pow2(s uint64) int { return 63 - bits.LeadingZeros64(s) }

// pointPath returns the root-to-leaf nodes that can contain point u.
func pointPath(u uint64) []nodeKey {
	keys := make([]nodeKey, 0, treeDepth+1)
	for level := 1; level <= treeDepth; level++ {
		d := 64 - level // block size exponent 63..0
		keys = append(keys, nodeKey{level: level, index: u >> d})
	}
	return keys
}

// add indexes one record.
func (idx *segmentIndex) add(rec *Record) {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	for _, k := range canonicalCover(toU(rec.Start), toU(rec.End)) {
		idx.chains[k] = append(idx.chains[k], chainEntry{Seq: rec.Seq, TxTime: rec.TxTime})
	}
}

// visibleSeq returns the Seq of the record visible at valid point v under
// cutoff c: among arrived records (Seq <= c.Seq), covering v, with TxTime
// <= c.T, choose greatest TxTime and then greatest Seq; seq==0 means unknown.
// inspected counts chain entries compared (O(log N) per chain, <=65 chains).
func (idx *segmentIndex) visibleSeq(v Tick, c Cutoff) (seq uint64, txTime Tick, inspected int) {
	bestSeq := uint64(0)
	var bestTx Tick
	found := false
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	for _, k := range pointPath(toU(v)) {
		chain := idx.chains[k]
		// Last position with Seq <= c.Seq.
		lo, hi := 0, len(chain)
		for lo < hi {
			mid := int(uint(lo+hi) >> 1)
			inspected++
			if chain[mid].Seq <= c.Seq {
				lo = mid + 1
			} else {
				hi = mid
			}
		}
		prefix := lo
		// Within the prefix TxTime is non-decreasing (monotonic clock); find
		// last position with TxTime <= c.T.
		lo, hi = 0, prefix
		for lo < hi {
			mid := int(uint(lo+hi) >> 1)
			inspected++
			if chain[mid].TxTime <= c.T {
				lo = mid + 1
			} else {
				hi = mid
			}
		}
		if lo == 0 {
			continue
		}
		e := chain[lo-1]
		if !found || e.TxTime > bestTx || (e.TxTime == bestTx && e.Seq > bestSeq) {
			bestTx, bestSeq = e.TxTime, e.Seq
			found = true
		}
	}
	idx.inspected += inspected
	return bestSeq, bestTx, inspected
}

// inspectCount returns cumulative chain entries examined (for tests).
func (idx *segmentIndex) inspectCount() int { return idx.inspected }
