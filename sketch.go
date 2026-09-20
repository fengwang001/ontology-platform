// Package quantile computes weighted quantiles of float64 samples held in
// process memory.
//
// Two exact definitions are provided and never mixed:
//
//   - QuantileNearestRank implements the nearest-rank method: the result is
//     always a value that actually occurs in the sample.
//   - QuantileLinear implements linear interpolation between consecutive
//     order statistics (Hyndman-Fan type 7 / R-7): the result may lie
//     between two samples.
package quantile

import "sync"

// point is one distinct sample value with its accumulated weight. +0.0 and
// -0.0 are merged into a single point whose value is reported as +0.0.
type point struct {
	value  float64
	weight uint64
}

// Sketch stores distinct sample values together with their integer weights.
// Adding a value w times is exactly equivalent to adding the same value once
// with weight w, without ever expanding the repetitions.
//
// A Sketch is safe for concurrent queries; queries never mutate internal
// state. Mixing concurrent Add calls with queries is supported as well.
type Sketch struct {
	mu         sync.RWMutex
	points     []point // sorted by value; +0.0 used for the zero bucket
	total      uint64
	skippedNaN uint64
}

// NewSketch returns an empty sketch.
func NewSketch() *Sketch {
	return &Sketch{}
}
