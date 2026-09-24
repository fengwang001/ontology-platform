// Package api is the public facade over rescale: a keyed state operator
// partitioned over key groups with minimal-movement rescaling.
package api

import (
	"ontology/kgrp"
	"ontology/rescale"
	"sync"
)

// The three failure classes are distinct sentinel errors.
var (
	ErrMaxPInvalid = kgrp.ErrMaxPInvalid
	ErrPInvalid    = kgrp.ErrPInvalid
	ErrEmptyKey    = kgrp.ErrEmptyKey
)

type Move struct{ KeyGroup, From int }
type InstancePlan struct {
	Start, End int    // [Start,End) is the new key-group interval
	Incoming   []Move // migrating groups, ascending by KeyGroup
}
type Plan struct {
	FromP, ToP, MovedKeys int
	Instances             []InstancePlan
}

// Engine is safe for concurrent use.
type Engine struct {
	mu   sync.RWMutex
	maxP int // immutable
	st   *rescale.Store
}

// New creates an engine with maxP key groups at parallelism p.
func New(maxP, p int) (*Engine, error) {
	st, e := rescale.New(maxP, p) // validates before allocating
	if e != nil {
		return nil, e
	}
	return &Engine{maxP: maxP, st: st}, nil
}

// Put sets key=val; an empty key is rejected with no state change.
func (e *Engine) Put(key string, val int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.st.Put(key, val)
}
func (e *Engine) Get(key string) (int64, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.st.Get(key)
}

// Owner returns the owning instance; -1 for an illegal (empty) key.
func (e *Engine) Owner(key string) int {
	kg, e0 := kgrp.KeyGroup(key, e.maxP)
	if e0 != nil {
		return -1
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	return kgrp.Owner(kg, e.st.P(), e.maxP)
}

// Ranges returns a fresh copy of the current instance intervals.
func (e *Engine) Ranges() [][2]int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.st.Ranges()
}

// Rescale changes parallelism to p2, moving only changed-owner groups. An
// illegal p2 is rejected before any state is touched.
func (e *Engine) Rescale(p2 int) (Plan, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	pl, err := e.st.Rescale(p2)
	if err != nil {
		return Plan{}, err
	}
	out := Plan{FromP: pl.FromP, ToP: pl.ToP, MovedKeys: pl.MovedKeys,
		Instances: make([]InstancePlan, len(pl.Instances))}
	for i, ip := range pl.Instances {
		out.Instances[i] = InstancePlan{Start: ip.Start, End: ip.End}
		for _, m := range ip.Incoming {
			out.Instances[i].Incoming = append(out.Instances[i].Incoming, Move{m.KeyGroup, m.From})
		}
	}
	return out, nil
}

// SelfCheck verifies the live layout and runs a built-in battery over internal
// maxP/p/key sets covering all four invariants.
func (e *Engine) SelfCheck() error {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if err := e.st.Check(); err != nil {
		return err
	}
	return battery()
}
func battery() error {
	if _, e := rescale.New(0, 1); e != ErrMaxPInvalid {
		return ErrMaxPInvalid // I4: maxP
	}
	for _, bp := range []int{0, 11} { // I4: p out of [1,maxP]
		if _, e := rescale.New(10, bp); e != ErrPInvalid {
			return ErrPInvalid
		}
	}
	keys := []string{"a", "bb", "ccc", "dddd", "u1", "u2", "u3", "u4", "u5", "u6", "u7", "u8"}
	for maxP := 1; maxP <= 64; maxP++ {
		for start := 1; start <= maxP; start++ {
			st, e := rescale.New(maxP, start)
			if e != nil || st.Check() != nil { // I1+I2
				return ErrPInvalid
			}
			for i, k := range keys {
				if e := st.Put(k, int64(i+1)); e != nil {
					return e
				}
			}
			if e := st.Put("", 1); e != ErrEmptyKey {
				return ErrEmptyKey // I4: empty key, no trace
			}
			cp := start
			for _, p2 := range []int{maxP, 1, start, cp} {
				if _, e := st.Rescale(0); e != ErrPInvalid || st.P() != cp {
					return ErrPInvalid // I4: rejected rescale leaves no trace
				}
				pl, e := st.Rescale(p2)
				if e != nil || st.Check() != nil {
					return ErrPInvalid
				}
				for i, k := range keys { // I3: key set and values conserved
					if gv, ok := st.Get(k); !ok || gv != int64(i+1) {
						return ErrPInvalid
					}
				}
				if p2 == cp && pl.MovedKeys != 0 { // p2==p1: zero moves
					return ErrPInvalid
				}
				cp = p2
			}
		}
	}
	return nil
}
