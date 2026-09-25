// Package api is the public facade of the change-log checksum service.
package api

import "ontology/verify"

var (
	ErrBadSegSize = verify.ErrBadSegSize
	ErrSeqGap     = verify.ErrSeqGap
	ErrBadRange   = verify.ErrBadRange
)

// Report is the outcome of SelfCheck.
type Report = verify.Report

// Log is a change log with segment checksums. Safe for concurrent use.
type Log struct {
	eng *verify.Engine
}

// New returns a Log whose segments span segSize records each.
func New(segSize int) (*Log, error) {
	eng, err := verify.New(segSize)
	if err != nil {
		return nil, err
	}
	return &Log{eng: eng}, nil
}

// Append adds (seq, val); seq must equal the previous seq + 1.
func (l *Log) Append(seq, val int64) error { return l.eng.Append(seq, val) }

// Verify returns the first corrupt seq in the closed range [from, to].
func (l *Log) Verify(from, to int64) (corrupt int64, ok bool, err error) {
	return l.eng.Verify(from, to)
}

// Recompute rebuilds the segment sums covering [from, to] and the total.
func (l *Log) Recompute(from, to int64) (segSums []int64, total int64, err error) {
	return l.eng.Recompute(from, to)
}

// Total returns the global checksum total.
func (l *Log) Total() int64 { return l.eng.Total() }

// SegSum returns the sum of 1-based segment k.
func (l *Log) SegSum(k int) int64 { return l.eng.SegSum(k) }

// SelfCheck verifies the four invariants on a built-in record sequence.
func (l *Log) SelfCheck() Report { return verify.SelfCheck() }
