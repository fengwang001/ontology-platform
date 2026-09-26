// Package vv implements version vectors as sparse actor id -> counter maps.
package vv

import "sync/atomic"

// Vector maps a non-negative actor id to a non-negative counter.
// A missing key is equivalent to counter 0.
type Vector map[int]int

// Relation is the outcome of a causal comparison between two vectors.
type Relation int

const (
	// Equal means A[k] == B[k] for every key.
	Equal Relation = iota
	// Less means A[k] <= B[k] for every key and A[k] < B[k] for some key.
	Less
	// Greater is the symmetric opposite of Less.
	Greater
	// Concurrent means neither A <= B nor A >= B.
	Concurrent
)

// mergeStats holds Merge-internal telemetry. Its field is unexported and
// never reachable through the public API; only in-package tests read it.
var mergeStats = struct {
	// reads is the number of vector entries read by the latest Merge.
	reads atomic.Int64
}{}

// Merge returns the join (key-wise max over the union of keys) of a and b.
// Missing keys are treated as 0. It is a pure function: neither input is
// mutated and the result is always a freshly allocated map.
func Merge(a, b Vector) Vector {
	reads := 0
	out := make(Vector, len(a)+len(b))
	for k, av := range a { // read each entry of a ...
		reads++
		bv := b[k] // ... and the corresponding entry of b (0 if absent)
		reads++
		if bv > av {
			out[k] = bv
		} else {
			out[k] = av
		}
	}
	for k, bv := range b { // scan b for keys absent from a
		reads++
		if _, ok := a[k]; ok {
			reads++
			continue
		}
		out[k] = bv
	}
	mergeStats.reads.Store(int64(reads))
	return out
}

// Compare returns the causal relation of a to b, treating missing keys as 0.
// Exactly one of Equal, Less, Greater, Concurrent is returned.
func Compare(a, b Vector) Relation {
	aLess, bLess := false, false
	for k, av := range a {
		switch bv := b[k]; {
		case av < bv:
			aLess = true
		case av > bv:
			bLess = true
		}
	}
	for k, bv := range b { // keys only b has: a's side is 0
		if _, ok := a[k]; ok {
			continue
		}
		if bv > 0 {
			aLess = true
		}
	}
	switch {
	case aLess && bLess:
		return Concurrent
	case aLess:
		return Less
	case bLess:
		return Greater
	default:
		return Equal
	}
}

// String renders the relation for external display.
func (r Relation) String() string {
	switch r {
	case Less:
		return "Less"
	case Greater:
		return "Greater"
	case Concurrent:
		return "Concurrent"
	default:
		return "Equal"
	}
}
