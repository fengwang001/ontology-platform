// Package bidx implements the offset/timestamp bidirectional index:
// forward TSAt(off) and reverse SafeOff(T) via binary search over the
// non-decreasing prefix-maximum sequence. It depends only on tsl.
package bidx

import (
	"errors"
	"sync/atomic"

	"ontology/tsl"
)

// Sentinel errors are distinct so callers can classify every failure.
var (
	ErrOutOfRange = errors.New("bidx: offset out of range")
	ErrEmptyLog   = errors.New("bidx: safeoff on empty log")
)

// Index is the append-only in-memory sequence. The zero value is not
// usable; construct with New.
type Index struct {
	ts  []int64      // raw timestamps in append order; ts[o] is the o-th Append
	pm  []int64      // prefix maxima; pm[o] = max(ts[0..o]), non-decreasing
	cmp atomic.Int64 // comparisons made by the most recent SafeOff binary search
}

func New() *Index { return &Index{} }

// Append assigns the next strictly increasing offset (0,1,2,...) and
// updates the prefix maximum incrementally.
func (x *Index) Append(ts int64) {
	var e tsl.Entry
	if len(x.ts) == 0 {
		e = tsl.First(ts)
	} else {
		e = tsl.Next(tsl.Entry{Off: int64(len(x.ts) - 1), PM: x.pm[len(x.pm)-1]}, ts)
	}
	x.ts = append(x.ts, e.TS)
	x.pm = append(x.pm, e.PM)
}

// TSAt returns the raw timestamp recorded at offset off.
func (x *Index) TSAt(off int64) (int64, error) {
	if off < 0 || off >= int64(len(x.ts)) {
		return 0, ErrOutOfRange
	}
	return x.ts[off], nil
}

// SafeOff returns the largest offset o for which every timestamp up to o
// is <= T, i.e. pm[o] <= T. When even the first record exceeds T it
// returns -1, false. On an empty log it returns ErrEmptyLog.
func (x *Index) SafeOff(T int64) (int64, bool, error) {
	x.cmp.Store(0)
	n := len(x.pm)
	if n == 0 {
		return 0, false, ErrEmptyLog
	}
	// Find the first index i with pm[i] > T; the answer is i-1.
	lo, hi := 0, n
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		x.cmp.Add(1)
		if x.pm[mid] <= T {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	if lo == 0 {
		return -1, false, nil
	}
	return int64(lo - 1), true, nil
}

func (x *Index) Len() int { return len(x.ts) }
