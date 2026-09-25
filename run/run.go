// Package run generates sorted runs from an input tuple stream:
// it chunks input by memory threshold M, sorts each chunk ascending,
// and spills it as a run.
package run

import (
	"errors"
	"sort"
)

// ErrNegativeKey is returned when an input tuple has Key < 0.
var ErrNegativeKey = errors.New("run: negative key")

// ErrBadThreshold is returned when memory threshold M < 1.
var ErrBadThreshold = errors.New("run: memory threshold M must be >= 1")

// Run is one spilled sorted chunk. ID is its spill order (0-based).
type Run struct {
	ID   int
	Keys []int // ascending
}

// Build validates keys, chunks them by M, sorts each chunk ascending
// and returns the runs in spill order. On any error it returns nil and
// leaves no partial result.
func Build(m int, keys []int) ([]Run, error) {
	if m < 1 {
		return nil, ErrBadThreshold
	}
	// Validate the whole batch before producing anything: a rejected
	// batch must leave no trace.
	for _, k := range keys {
		if k < 0 {
			return nil, ErrNegativeKey
		}
	}
	var runs []Run
	for start := 0; start < len(keys); start += m {
		end := start + m
		if end > len(keys) {
			end = len(keys)
		}
		chunk := make([]int, end-start)
		copy(chunk, keys[start:end])
		sort.Ints(chunk)
		runs = append(runs, Run{ID: len(runs), Keys: chunk})
	}
	return runs, nil
}
