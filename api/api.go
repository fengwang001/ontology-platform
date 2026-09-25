// Package api is the public face of the change-log checksum service.
package api

import (
	"fmt"

	"ontology/cksum"
	"ontology/verify"
)

// Sentinel errors re-exported from verify for callers of this package.
var (
	ErrBadSegSize = verify.ErrBadSegSize
	ErrSeqGap     = verify.ErrSeqGap
	ErrBadRange   = verify.ErrBadRange
)

// Log is the public handle to a checksummed change log.
type Log struct{ l *verify.Log }

// New creates a log whose segments hold segSize records each.
func New(segSize int) (*Log, error) {
	l, err := verify.NewLog(segSize)
	if err != nil {
		return nil, err
	}
	return &Log{l: l}, nil
}

// Append adds one record; seq must be exactly previous Seq + 1.
func (a *Log) Append(seq, val int64) error { return a.l.Append(seq, val) }

// Verify locates the first corrupted record in the closed range [from, to].
func (a *Log) Verify(from, to int64) (corrupt int64, ok bool, err error) {
	return a.l.Verify(from, to)
}

// Recompute rebuilds segment sums and the global total over [from, to].
func (a *Log) Recompute(from, to int64) (segSums []int64, total int64, err error) {
	return a.l.Recompute(from, to)
}

// Total returns the global checksum total.
func (a *Log) Total() int64 { return a.l.Total() }

// SegSum returns the sum of segment k (1-based).
func (a *Log) SegSum(k int) (int64, error) { return a.l.SegSum(k) }

// Corrupt injects a fault: overwrite Val of seq, keep its saved checksum.
func (a *Log) Corrupt(seq, val int64) error { return a.l.Corrupt(seq, val) }

// SelfCheck runs a built-in drill over a fixed record sequence and checks
// the four invariants. It is safe for concurrent use and returns nil on
// success.
func SelfCheck() error {
	l, err := New(3)
	if err != nil {
		return err
	}
	orig := []int64{10, 20, 30, 40, 50, 60}
	var wantTotal int64
	for i, v := range orig {
		if err := l.Append(int64(i+1), v); err != nil {
			return err
		}
		wantTotal += cksum.Elem(int64(i+1), v)
		if l.Total() != wantTotal { // invariant 2: total grows by exactly e
			return fmt.Errorf("selfcheck: total drift after append %d", i+1)
		}
	}
	if err := l.Corrupt(4, 70); err != nil {
		return err
	}
	if err := l.Corrupt(5, 80); err != nil {
		return err
	}
	// Invariant 1: naive reference = first seq whose current e differs
	// from the e of the originally appended value, scanned ascending.
	var naive int64
	for i, v := range orig {
		cur := v
		if i == 3 {
			cur = 70
		}
		if i == 4 {
			cur = 80
		}
		if cksum.Elem(int64(i+1), cur) != cksum.Elem(int64(i+1), v) {
			naive = int64(i + 1)
			break
		}
	}
	got, ok, err := l.Verify(1, 6)
	if err != nil || ok || got != naive { // invariants 1 and 3
		return fmt.Errorf("selfcheck: verify got (%d,%v), want first corrupt %d", got, ok, naive)
	}
	// Invariant 4: rejected operations leave no trace.
	before := l.Total()
	if err := l.Append(9, 90); err != ErrSeqGap {
		return fmt.Errorf("selfcheck: gap append err=%v", err)
	}
	if _, _, err := l.Verify(0, 2); err != ErrBadRange {
		return fmt.Errorf("selfcheck: bad range err=%v", err)
	}
	if l.Total() != before {
		return fmt.Errorf("selfcheck: rejected op changed total")
	}
	return nil
}
