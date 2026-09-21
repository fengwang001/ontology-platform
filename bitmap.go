// Package ontology implements a run-length encoded (RLE) bitmap set over
// the uint32 universe. All state lives in process memory.
//
// A set is stored as a list of alternating zero/one runs. The canonical
// (normalized) form guarantees a unique encoding for every distinct set:
//
//   - adjacent runs always alternate in value (same-value runs are merged);
//   - no run has length 0;
//   - no trailing zero run is kept (the last run, if any, is a one-run).
//
// The empty set is therefore the empty run list, and its encoding is the
// single byte 0x00 (a uvarint run count of 0) — deterministic and shortest.
//
// Run lengths are uint64 internally, so a single run may span the whole
// 2^32 universe (e.g. the full set has one one-run of length 2^32).
package ontology

import (
	"sort"
	"sync"
)

// run is a single maximal segment of bits with the same value.
type run struct {
	one    bool   // bit value held by this run
	length uint64 // number of bits, always >= 1, may be up to 2^32
}

// Bitmap is a concurrency-safe RLE set over the uint32 universe.
// The zero value is ready to use and represents the empty set.
type Bitmap struct {
	mu   sync.RWMutex
	runs []run
	ends []uint64 // ends[i] = total bits covered by runs[0..i] (exclusive end)
}

// New returns an empty Bitmap.
func New() *Bitmap { return &Bitmap{} }

// normalizeLocked merges adjacent same-value runs, drops zero-length runs
// and strips trailing zero runs, then rebuilds the prefix-sum index.
// Callers must hold b.mu (write).
func (b *Bitmap) normalizeLocked() {
	src := b.runs
	out := src[:0]
	for _, r := range src {
		if r.length == 0 {
			continue
		}
		if n := len(out); n > 0 && out[n-1].one == r.one {
			out[n-1].length += r.length
			continue
		}
		out = append(out, r)
	}
	for len(out) > 0 && !out[len(out)-1].one {
		out = out[:len(out)-1]
	}
	b.runs = out
	b.rebuildEndsLocked()
}

// rebuildEndsLocked recomputes the exclusive-end prefix sums.
// Callers must hold b.mu.
func (b *Bitmap) rebuildEndsLocked() {
	if cap(b.ends) < len(b.runs) {
		b.ends = make([]uint64, len(b.runs))
	}
	b.ends = b.ends[:len(b.runs)]
	var sum uint64
	for i, r := range b.runs {
		sum += r.length
		b.ends[i] = sum
	}
}

// findLocked returns the index of the run containing absolute position pos,
// or len(runs) if pos lies in the implicit trailing zero region.
// Callers must hold b.mu.
func (b *Bitmap) findLocked(pos uint64) int {
	return sort.Search(len(b.ends), func(i int) bool { return b.ends[i] > pos })
}

// runStartLocked returns the absolute start position of run i.
func (b *Bitmap) runStartLocked(i int) uint64 {
	if i == 0 {
		return 0
	}
	return b.ends[i-1]
}

// Runs returns the number of runs in the canonical encoding. It is mainly
// useful for tests and diagnostics.
func (b *Bitmap) Runs() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.runs)
}
