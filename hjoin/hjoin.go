// Package hjoin implements the partitioned build side and probe side of a
// hash join, including rescan of spilled partitions. It depends only on part.
package hjoin

import (
	"sync/atomic"

	"ontology/part"
)

// Pair is one (probe key, build key) match; equal keys by construction.
type Pair struct {
	P part.Key
	B part.Key
}

// partition is one of the n build partitions. When its build tuple count
// exceeds m, all tuples go to spill and mem stays empty; probe must then
// rescan spill (together with mem) to stay correct.
type partition struct {
	mem     map[part.Key]int // build key -> multiplicity, held in memory
	spill   map[part.Key]int // build key -> multiplicity, held in spill area
	spilled bool
}

// Join holds the partitioned build hash tables and spill areas.
type Join struct {
	n int
	m int

	// partitionsChecked is the unexported counter required by the task:
	// number of partitions inspected while locating the most recent probe
	// key. Direct h-location always costs 1; it is never exposed through
	// any exported function or method. Atomic so concurrent Probe calls
	// stay race-free.
	partitionsChecked atomic.Int32

	parts []partition
}

// New creates an empty join with n partitions and per-partition memory
// threshold m. Callers are expected to validate n >= 2 and m >= 1.
func New(n, m int) *Join {
	return &Join{n: n, m: m, parts: make([]partition, n)}
}

// Build replaces the build relation: every key is assigned to exactly one
// partition via the shared partition function part.H, and a partition whose
// build tuple count exceeds m spills all of its tuples.
func (j *Join) Build(keys []part.Key) {
	counts := make([]map[part.Key]int, j.n)
	totals := make([]int, j.n)
	for _, k := range keys {
		p := part.H(k, j.n) // same h as probe
		if counts[p] == nil {
			counts[p] = make(map[part.Key]int)
		}
		counts[p][k]++
		totals[p]++
	}
	next := make([]partition, j.n)
	for p := 0; p < j.n; p++ {
		if part.Spilled(totals[p], j.m) {
			next[p] = partition{spill: counts[p], spilled: true}
		} else {
			next[p] = partition{mem: counts[p]}
		}
	}
	j.parts = next
}

// Probe walks keys in order and emits, for each probe key, one pair per
// matching build tuple, so duplicate keys expand as the product of counts.
// A spilled partition is found by rescanning its spill area together with
// the in-memory hash table.
func (j *Join) Probe(keys []part.Key) []Pair {
	var out []Pair
	for _, k := range keys {
		p := part.H(k, j.n) // locate directly by h: exactly one partition
		j.partitionsChecked.Store(1)
		pt := &j.parts[p]
		if pt.spilled {
			out = emit(out, k, pt.spill)
			out = emit(out, k, pt.mem)
		} else {
			out = emit(out, k, pt.mem)
		}
	}
	return out
}

// emit appends one (k, k) pair per build tuple carrying key k.
func emit(out []Pair, k part.Key, table map[part.Key]int) []Pair {
	for c := table[k]; c > 0; c-- {
		out = append(out, Pair{P: k, B: k})
	}
	return out
}
