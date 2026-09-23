// Package vv defines version vectors and their causal partial order.
package vv

import (
	"errors"
	"math"
	"sort"
)

// Relation describes how one version vector relates to another.
type Relation int

const (
	Equal Relation = iota
	Before
	After
	Concurrent
)

func (r Relation) String() string {
	switch r {
	case Equal:
		return "equal"
	case Before:
		return "before"
	case After:
		return "after"
	default:
		return "concurrent"
	}
}

// Vector is a sparse map from replica ID to event counter.
type Vector map[string]uint64

// Stats records the number of component comparisons made by Compare.
type Stats struct {
	Comparisons int
}

var (
	// ErrUnknownReplica means an ID is not present in the registry.
	ErrUnknownReplica = errors.New("unknown replica")
	// ErrOverflow means a counter cannot be incremented without wrapping.
	ErrOverflow = errors.New("version counter overflow")
)

// Clone returns an independent copy of v.
func (v Vector) Clone() Vector {
	out := make(Vector, len(v))
	for id, count := range v {
		out[id] = count
	}
	return out
}

// Keys returns the deterministic sorted union of component IDs.
func Keys(a, b Vector) []string {
	seen := make(map[string]struct{}, len(a)+len(b))
	for id := range a {
		seen[id] = struct{}{}
	}
	for id := range b {
		seen[id] = struct{}{}
	}
	keys := make([]string, 0, len(seen))
	for id := range seen {
		keys = append(keys, id)
	}
	sort.Strings(keys)
	return keys
}

// Compare computes the partial order. Missing components count as zero.
func Compare(a, b Vector) Relation {
	r, _ := CompareStats(a, b)
	return r
}

// CompareStats is Compare with component-comparison accounting.
func CompareStats(a, b Vector) (Relation, Stats) {
	hasLess, hasGreater := false, false
	n := 0
	for _, id := range Keys(a, b) {
		n++
		if a[id] < b[id] {
			hasLess = true
		}
		if a[id] > b[id] {
			hasGreater = true
		}
	}
	switch {
	case !hasLess && !hasGreater:
		return Equal, Stats{Comparisons: n}
	case hasLess && !hasGreater:
		return Before, Stats{Comparisons: n}
	case !hasLess && hasGreater:
		return After, Stats{Comparisons: n}
	default:
		return Concurrent, Stats{Comparisons: n}
	}
}

// Increment clones v and increments id, refusing uint64 wrap.
func Increment(v Vector, id string) (Vector, error) {
	if v[id] == math.MaxUint64 {
		return nil, ErrOverflow
	}
	out := v.Clone()
	out[id]++
	return out, nil
}

// ValidateRegistry reports any component that is not registered.
func ValidateRegistry(v Vector, registered map[string]struct{}) error {
	for id := range v {
		if _, ok := registered[id]; !ok {
			return ErrUnknownReplica
		}
	}
	return nil
}
