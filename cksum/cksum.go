// Package cksum computes per-record element checksums and segment sums
// for the change log. It depends on nothing else.
package cksum

// Elem returns the element checksum e(Seq, Val) = Seq*100 + Val.
func Elem(seq, val int64) int64 { return seq*100 + val }

// Accum accumulates element checksums for one segment.
type Accum struct{ sum int64 }

// Add folds one record's element checksum into the accumulator.
func (a *Accum) Add(seq, val int64) { a.sum += Elem(seq, val) }

// Sum returns the accumulated segment sum.
func (a *Accum) Sum() int64 { return a.sum }
