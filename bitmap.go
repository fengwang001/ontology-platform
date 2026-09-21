// Package ontology implements a run-length encoded (RLE) bitmap set over
// the uint32 universe. The set keeps only 1-runs in memory and guarantees
// a canonical (unique) encoding for every distinct set.
package ontology

import (
	"sort"
	"sync"
)

// domainSize is the number of representable bits: 2^32.
const domainSize = uint64(1) << 32

// run represents a maximal consecutive run of set bits:
// [start, start+length-1]. length is uint64 because a single run may
// cover the whole 2^32 domain (length == 1<<32 does not fit in uint32).
type run struct {
	start  uint32
	length uint64
}

// end returns the last set bit of the run as uint64 to avoid overflow
// when the run reaches math.MaxUint32.
func (r run) end() uint64 {
	return uint64(r.start) + r.length - 1
}

// Bitmap is a set of uint32 values stored as sorted, disjoint,
// non-adjacent 1-runs. Invariants (checked by Verify):
//  1. every run has length >= 1 (no zero-length runs);
//  2. runs are strictly ordered with at least one 0-bit gap between
//     them (adjacent same-value runs are always merged);
//  3. no trailing 0-run is ever stored (only 1-runs exist).
type Bitmap struct {
	mu   sync.RWMutex
	runs []run
}

// New returns an empty Bitmap.
func New() *Bitmap {
	return &Bitmap{}
}

// snapshot returns a copy of the run slice under a read lock, so set
// operations can merge without holding locks or risking deadlock.
func (b *Bitmap) snapshot() []run {
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := make([]run, len(b.runs))
	copy(out, b.runs)
	return out
}

// searchRun returns the index of the first run whose end() >= bit.
func searchRun(runs []run, bit uint32) int {
	return sort.Search(len(runs), func(i int) bool {
		return runs[i].end() >= uint64(bit)
	})
}

// Contains reports whether bit is in the set.
func (b *Bitmap) Contains(bit uint32) bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	i := searchRun(b.runs, bit)
	return i < len(b.runs) && uint64(b.runs[i].start) <= uint64(bit)
}

// Set adds bit to the set. It is idempotent: setting an already-set bit
// leaves the canonical encoding unchanged.
func (b *Bitmap) Set(bit uint32) {
	b.mu.Lock()
	defer b.mu.Unlock()
	runs := b.runs
	i := searchRun(runs, bit)
	if i < len(runs) && uint64(runs[i].start) <= uint64(bit) {
		return // already inside a run
	}
	bit64 := uint64(bit)
	mergePrev := i > 0 && runs[i-1].end()+1 == bit64
	mergeNext := i < len(runs) && uint64(runs[i].start) == bit64+1
	switch {
	case mergePrev && mergeNext:
		// Bridge two runs: extend previous over bit and swallow next.
		runs[i-1].length = uint64(runs[i].start) + runs[i].length - uint64(runs[i-1].start)
		b.runs = append(runs[:i], runs[i+1:]...)
	case mergePrev:
		runs[i-1].length++
	case mergeNext:
		runs[i].start = bit
		runs[i].length++
	default:
		b.runs = append(runs, run{})
		copy(b.runs[i+1:], runs[i:])
		b.runs[i] = run{start: bit, length: 1}
	}
}

// Clear removes bit from the set. It is idempotent: clearing an unset
// bit leaves the canonical encoding unchanged.
func (b *Bitmap) Clear(bit uint32) {
	b.mu.Lock()
	defer b.mu.Unlock()
	runs := b.runs
	i := searchRun(runs, bit)
	if i == len(runs) || uint64(runs[i].start) > uint64(bit) {
		return // not set
	}
	r := runs[i]
	bit64 := uint64(bit)
	switch {
	case r.length == 1:
		b.runs = append(runs[:i], runs[i+1:]...)
	case uint64(r.start) == bit64:
		runs[i].start++
		runs[i].length--
	case r.end() == bit64:
		runs[i].length--
	default:
		// Split into [start, bit-1] and [bit+1, end].
		right := run{start: bit + 1, length: r.end() - bit64}
		runs[i].length = bit64 - uint64(r.start)
		b.runs = append(runs, run{})
		copy(b.runs[i+2:], runs[i+1:])
		b.runs[i+1] = right
	}
}
