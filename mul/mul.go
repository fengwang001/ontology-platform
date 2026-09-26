// Package mul provides naive and blocked square-matrix multiplication for
// row-major float64 data. The blocked variant groups loops by blocks but
// preserves the naive accumulation order, so its float64 results are
// bit-for-bit identical to Naive.
package mul

import (
	"errors"

	"ontology/blk"
)

// ErrShape is returned when inputs are not a valid n*n multiplication.
var ErrShape = errors.New("mul: n must be >= 1 and a,b must each have length n*n")

// valid checks the shared preconditions; b is the matrix argument.
func valid(a, b []float64, n int) bool {
	return n >= 1 && len(a) == n*n && len(b) == n*n
}

// Naive computes C = a*b with the plain three-loop reference. For fixed
// (i,j) the dot product accumulates k in strictly ascending order.
// It panics via ErrShape-shaped guard only when called directly with bad
// shapes; callers in package api validate first.
func Naive(a, b []float64, n int) []float64 {
	if !valid(a, b, n) {
		panic(ErrShape)
	}
	c := make([]float64, n*n)
	for i := 0; i < n; i++ {
		row := i * n
		for k := 0; k < n; k++ {
			aik := a[row+k]
			krow := k * n
			for j := 0; j < n; j++ {
				c[row+j] += aik * b[krow+j]
			}
		}
	}
	return c
}

// Blocked computes C = a*b grouped by blocks of width blkSize. The outer
// loops traverse i-blocks, then k-blocks, then j-blocks; tail blocks of
// size n%b come from blk.Parts and are multiplied like any other.
//
// For any fixed (i,j), writes to c[i,j] happen as the k-blocks are
// visited in ascending order with k ascending inside each block, hence
// the accumulation order is exactly 0,1,...,n-1 and the result is
// bit-identical to Naive regardless of signs or zero values.
func Blocked(a, b []float64, n, blkSize int) []float64 {
	if !valid(a, b, n) || blkSize < 1 || blkSize > n {
		panic(ErrShape)
	}
	ib := blk.Parts(n, blkSize)
	kb := blk.Parts(n, blkSize)
	jb := blk.Parts(n, blkSize)
	c := make([]float64, n*n)
	for _, pi := range ib {
		for _, pk := range kb {
			for _, pj := range jb {
				for i := pi.Start; i < pi.End; i++ {
					row := i * n
					for k := pk.Start; k < pk.End; k++ {
						aik := a[row+k]
						krow := k * n
						for j := pj.Start; j < pj.End; j++ {
							c[row+j] += aik * b[krow+j]
						}
					}
				}
			}
		}
	}
	return c
}
