// Package bitmap implements a run-length encoded set over the uint32
// universe with canonical (unique) encoding and compressed-domain
// set operations.
//
// Encoding (produced by Bytes):
//
//	The encoding is a sequence of runs. Each run is encoded as one
//	value byte (0 or 1) followed by the run length as a uvarint
//	(encoding/binary.AppendUvarint). Run lengths are uint64, so a
//	single run may span the whole universe (length 2^32, which does
//	not fit in a uint32). The empty set encodes to the empty byte
//	slice: deterministic and shortest possible.
//
// Canonical form (guaranteed by every mutating operation):
//
//  1. Adjacent runs always alternate values; equal-value neighbours
//     are merged.
//  2. No run has length 0.
//  3. The last run is never a 0-run (trailing zeros are implicit).
//
// Because of these invariants, any two Bitmaps representing the same
// set produce byte-identical output from Bytes.
package bitmap

import (
	"sort"
	"sync"
)

// run is one maximal block of equal-valued bits.
// length is uint64 because a run may cover the whole uint32 universe
// (length 2^32).
type run struct {
	val    uint8 // 0 or 1
	length uint64
}

// Bitmap is a run-length encoded set of uint32 values.
// The zero value is ready to use and represents the empty set.
type Bitmap struct {
	mu   sync.RWMutex
	runs []run
	// totalLen is the sum of all run lengths, i.e. the position one
	// past the last covered bit. Maintained by fill.
	totalLen uint64
	// lastStatRuns records how many runs the most recent
	// Count/Min/Max call inspected. Readable via LastStatRuns.
	lastStatRuns int
}

// New returns an empty Bitmap.
func New() *Bitmap { return &Bitmap{} }

// normalize merges adjacent equal-valued runs, drops zero-length
// runs and drops a trailing 0-run. It returns rs in canonical form.
func normalize(rs []run) []run {
	out := rs[:0]
	for _, r := range rs {
		if r.length == 0 {
			continue
		}
		if n := len(out); n > 0 && out[n-1].val == r.val {
			out[n-1].length += r.length
			continue
		}
		out = append(out, r)
	}
	if n := len(out); n > 0 && out[n-1].val == 0 {
		out = out[:n-1]
	}
	return out
}

// totalOf returns the sum of all run lengths.
func totalOf(rs []run) uint64 {
	var t uint64
	for _, r := range rs {
		t += r.length
	}
	return t
}

// ends returns the exclusive end position of every run.
func ends(rs []run) []uint64 {
	e := make([]uint64, len(rs))
	var pos uint64
	for i, r := range rs {
		pos += r.length
		e[i] = pos
	}
	return e
}

// findRun returns the index of the run containing pos, and that
// run's start position. ok is false when pos lies beyond every run
// (implicit trailing zeros).
func findRun(rs []run, pos uint64) (idx int, start uint64, ok bool) {
	e := ends(rs)
	i := sort.Search(len(e), func(i int) bool { return e[i] > pos })
	if i == len(e) {
		return 0, 0, false
	}
	return i, e[i] - rs[i].length, true
}

// fill sets every bit in [lo, hi] (inclusive) to val.
// Callers must hold b.mu.
func (b *Bitmap) fill(lo, hi uint32, val uint8) {
	lo64, hi64 := uint64(lo), uint64(hi)
	i, startI, okI := findRun(b.runs, lo64)
	if !okI {
		// [lo, hi] lies entirely in the implicit trailing zeros.
		if val == 0 {
			return
		}
		if gap := lo64 - b.totalLen; gap > 0 {
			b.runs = append(b.runs, run{val: 0, length: gap})
		}
		b.runs = append(b.runs, run{val: val, length: hi64 - lo64 + 1})
		b.runs = normalize(b.runs)
		b.totalLen = totalOf(b.runs)
		return
	}
	j, startJ, okJ := findRun(b.runs, hi64)
	var endJ uint64
	if okJ {
		endJ = startJ + b.runs[j].length
	} else {
		// hi lies in the implicit trailing zeros.
		j = len(b.runs) - 1
		endJ = b.totalLen
	}

	next := make([]run, 0, len(b.runs)+2)
	next = append(next, b.runs[:i]...)
	if lo64 > startI {
		next = append(next, run{val: b.runs[i].val, length: lo64 - startI})
	}
	next = append(next, run{val: val, length: hi64 - lo64 + 1})
	if hi64+1 < endJ {
		next = append(next, run{val: b.runs[j].val, length: endJ - hi64 - 1})
	}
	next = append(next, b.runs[j+1:]...)
	b.runs = normalize(next)
	b.totalLen = totalOf(b.runs)
}

// Set adds x to the set. Setting a bit that is already 1 is a no-op
// and never degrades the encoding.
func (b *Bitmap) Set(x uint32) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.fill(x, x, 1)
}

// Clear removes x from the set. Clearing a bit that is already 0 is
// a no-op and never degrades the encoding.
func (b *Bitmap) Clear(x uint32) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.fill(x, x, 0)
}

// SetRange adds every value in [lo, hi] (inclusive) to the set.
func (b *Bitmap) SetRange(lo, hi uint32) {
	if lo > hi {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.fill(lo, hi, 1)
}

// ClearRange removes every value in [lo, hi] (inclusive) from the set.
func (b *Bitmap) ClearRange(lo, hi uint32) {
	if lo > hi {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.fill(lo, hi, 0)
}

// Contains reports whether x is in the set.
func (b *Bitmap) Contains(x uint32) bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	i, _, ok := findRun(b.runs, uint64(x))
	return ok && b.runs[i].val == 1
}

// runsSnapshot returns a copy of the canonical run list.
func (b *Bitmap) runsSnapshot() []run {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return append([]run(nil), b.runs...)
}
