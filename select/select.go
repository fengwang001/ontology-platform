// Package select (Go identifier selectx) fetches the values of a sorted
// population at the indices computed by package samp.
//
// It depends only on samp. A Selector touches exactly s population elements
// per Sample call — it indexes directly, never scans — and records that
// number in the unexported accessed field.
package selectx

import (
	"errors"
	"sync"

	"ontology/samp"
)

// ErrPopulationLengthMismatch reports that the population passed to Sample
// does not have the length N the selector was constructed for.
var ErrPopulationLengthMismatch = errors.New("select: population length does not equal N")

// Selector pulls sample values out of an int64 population using a Plan.
type Selector struct {
	mu   sync.Mutex
	plan *samp.Plan

	// accessed counts population elements visited while building the most
	// recent successful Sample. Unexported on purpose: no exported method or
	// function exposes it; only in-package tests read it.
	accessed int
}

// New builds a Selector over the given plan.
func New(plan *samp.Plan) *Selector {
	return &Selector{plan: plan}
}

// get is the single point of population access, so the counter genuinely
// reflects elements visited rather than being a constant written afterwards.
func (sel *Selector) get(vals []int64, i int) int64 {
	sel.accessed++
	return vals[i]
}

// Sample returns the s values located at the plan's indices. It validates
// len(vals) before touching any state, so a rejected call changes neither
// the result nor the access counter.
func (sel *Selector) Sample(vals []int64) ([]int64, error) {
	sel.mu.Lock()
	defer sel.mu.Unlock()
	if len(vals) != sel.plan.N() {
		return nil, ErrPopulationLengthMismatch
	}
	idx := sel.plan.Indices()
	out := make([]int64, len(idx))
	sel.accessed = 0
	for i, at := range idx {
		out[i] = sel.get(vals, at)
	}
	return out, nil
}

// accessedCount reports the counter; unexported, for in-package tests only.
func (sel *Selector) accessedCount() int {
	sel.mu.Lock()
	defer sel.mu.Unlock()
	return sel.accessed
}

// VerifyLastSampleTookExactlyS checks the fixed complexity invariant for the
// most recent Sample: it visited exactly s population elements. It takes no
// argument and returns only a boolean for this one fixed property, so the
// counter's numeric value cannot be read (or probed) through the public API;
// the number itself is readable solely from in-package tests.
func (sel *Selector) VerifyLastSampleTookExactlyS() bool {
	sel.mu.Lock()
	defer sel.mu.Unlock()
	return sel.accessed == sel.plan.Size()
}
