// Package cksum computes the per-record element checksum and accumulates
// segment sums. It depends on no other package in this module.
package cksum

// Elem is the element checksum e(Seq, Val) = 100*Seq + Val:
// Seq has weight 100, Val has weight 1.
func Elem(seq, val int64) int64 {
	return seq*100 + val
}

// Seg is a running accumulator for the sum of element checksums
// belonging to one segment.
type Seg struct {
	sum int64
}

// Add accumulates one element checksum into the segment.
func (s *Seg) Add(e int64) {
	s.sum += e
}

// Sum returns the current segment sum.
func (s *Seg) Sum() int64 {
	return s.sum
}

// Set overwrites the segment sum; used when a segment is rebuilt
// from its records during recomputation.
func (s *Seg) Set(v int64) {
	s.sum = v
}
