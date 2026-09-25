// Package verify maintains segment sums and the global total of a change
// log, verifies ranges, and locates the first corrupted record.
package verify

import (
	"errors"
	"sync"
	"sync/atomic"

	"ontology/cksum"
)

// Sentinel errors, each individually distinguishable via errors.Is.
var (
	ErrBadSegSize = errors.New("verify: segment size must be positive")
	ErrSeqGap     = errors.New("verify: seq is not contiguous")
	ErrBadRange   = errors.New("verify: invalid range")
)

// Log is an in-memory change log with segment-level checksums.
type Log struct {
	mu      sync.RWMutex
	segSize int64
	vals    []int64 // vals[i] is the Val of Seq i+1
	es      []int64 // element checksums saved at Append time
	segs    []int64 // segs[k] is the sum of segment k+1
	total   int64
	checked atomic.Int64 // records compared in the most recent Verify
}

// NewLog creates an empty log; segSize <= 0 is rejected with ErrBadSegSize.
func NewLog(segSize int) (*Log, error) {
	if segSize <= 0 {
		return nil, ErrBadSegSize
	}
	return &Log{segSize: int64(segSize)}, nil
}

// Append adds one record; seq must be exactly previous Seq + 1.
func (l *Log) Append(seq, val int64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if seq != int64(len(l.vals))+1 {
		return ErrSeqGap
	}
	e := cksum.Elem(seq, val)
	l.vals = append(l.vals, val)
	l.es = append(l.es, e)
	k := (seq - 1) / l.segSize
	for int64(len(l.segs)) <= k {
		l.segs = append(l.segs, 0)
	}
	l.segs[k] += e
	l.total += e
	return nil
}

// Corrupt overwrites the stored Val of seq without touching its saved
// checksum: fault injection for drills and tests.
func (l *Log) Corrupt(seq, val int64) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if seq < 1 || seq > int64(len(l.vals)) {
		return ErrBadRange
	}
	l.vals[seq-1] = val
	return nil
}

// Verify recomputes e over the closed range [from, to] and returns the
// smallest Seq whose saved checksum mismatches; ok is true when none do.
func (l *Log) Verify(from, to int64) (corrupt int64, ok bool, err error) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if err := l.checkRange(from, to); err != nil {
		return 0, false, err
	}
	var n int64
	defer func() { l.checked.Store(n) }()
	for seq := from; seq <= to; seq++ {
		n++
		if cksum.Elem(seq, l.vals[seq-1]) != l.es[seq-1] {
			return seq, false, nil
		}
	}
	return 0, true, nil
}

// Recompute rebuilds the segment sums of every segment intersecting
// [from, to] from current values, and rebuilds the global total.
func (l *Log) Recompute(from, to int64) (segSums []int64, total int64, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.checkRange(from, to); err != nil {
		return nil, 0, err
	}
	n := int64(len(l.vals))
	for k := (from - 1) / l.segSize; k <= (to-1)/l.segSize; k++ {
		var a cksum.Accum
		for seq := k*l.segSize + 1; seq <= (k+1)*l.segSize && seq <= n; seq++ {
			a.Add(seq, l.vals[seq-1])
		}
		l.segs[k] = a.Sum()
	}
	l.total = 0
	for _, s := range l.segs {
		l.total += s
	}
	out := make([]int64, len(l.segs))
	copy(out, l.segs)
	return out, l.total, nil
}

// Total returns the global checksum total.
func (l *Log) Total() int64 {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.total
}

// SegSum returns the sum of segment k (1-based).
func (l *Log) SegSum(k int) (int64, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if k < 1 || k > len(l.segs) {
		return 0, ErrBadRange
	}
	return l.segs[k-1], nil
}

// Len returns the number of appended records.
func (l *Log) Len() int64 {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return int64(len(l.vals))
}

func (l *Log) checkRange(from, to int64) error {
	if from < 1 || from > to || to > int64(len(l.vals)) {
		return ErrBadRange
	}
	return nil
}
