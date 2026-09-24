// Package rescale stores per-key state in instance/key-group buckets and
// redistributes buckets when parallelism changes. Depends only on kgrp.
package rescale

import (
	"sync"

	"ontology/kgrp"
)

// Move describes one incoming key group in a migration plan.
type Move struct {
	KeyGroup int // migrating key group
	From     int // source (old) instance
}

// InstancePlan is the new range of an instance after rescaling, along with
// the key groups that migrate into it, ordered by key-group number.
type InstancePlan struct {
	Instance int
	Start    int
	End      int
	Incoming []Move
}

// Plan lists per-instance plans ordered by new instance number.
type Plan []InstancePlan

// State is the keyed state distributed over p instances via key groups.
type State struct {
	mu sync.RWMutex

	maxP    int
	p       int
	buckets [][]map[string]int64 // buckets[instance][keyGroup] -> entries

	// lastVisited counts entries touched by the latest Rescale: only
	// entries of migrating key groups. Unexported; in-package tests only.
	lastVisited int
}

// New validates parameters and creates empty state with parallelism p.
func New(maxP, p int) (*State, error) {
	if err := kgrp.CheckP(maxP, p); err != nil {
		return nil, err
	}
	s := &State{maxP: maxP, p: p}
	s.buckets = s.alloc(p)
	return s, nil
}

func (s *State) alloc(p int) [][]map[string]int64 {
	b := make([][]map[string]int64, p)
	for i := range b {
		b[i] = make([]map[string]int64, s.maxP)
	}
	return b
}

// MaxP and P report the current configuration.
func (s *State) MaxP() int { s.mu.RLock(); defer s.mu.RUnlock(); return s.maxP }
func (s *State) P() int    { s.mu.RLock(); defer s.mu.RUnlock(); return s.p }

// Put inserts or updates one key. Empty keys are rejected.
func (s *State) Put(key string, val int64) error {
	if key == "" {
		return kgrp.ErrEmptyKey
	}
	kg := int(kgrp.Hash(key) % uint32(s.maxP))
	s.mu.Lock()
	defer s.mu.Unlock()
	inst := kgrp.Instance(kg, s.p, s.maxP)
	m := s.buckets[inst][kg]
	if m == nil {
		m = make(map[string]int64)
		s.buckets[inst][kg] = m
	}
	m[key] = val
	return nil
}

// Get returns the value of key and whether it exists.
func (s *State) Get(key string) (int64, bool) {
	if key == "" {
		return 0, false
	}
	kg := int(kgrp.Hash(key) % uint32(s.maxP))
	s.mu.RLock()
	defer s.mu.RUnlock()
	inst := kgrp.Instance(kg, s.p, s.maxP)
	m := s.buckets[inst][kg]
	v, ok := m[key]
	return v, ok
}

// Owner reports the instance owning key.
func (s *State) Owner(key string) int {
	kg := int(kgrp.Hash(key) % uint32(s.maxP))
	s.mu.RLock()
	defer s.mu.RUnlock()
	return kgrp.Instance(kg, s.p, s.maxP)
}

// Ranges returns the half-open key-group ranges of all instances.
func (s *State) Ranges() [][2]int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([][2]int, s.p)
	for i := 0; i < s.p; i++ {
		st, en := kgrp.RangeBounds(i, s.p, s.maxP)
		out[i] = [2]int{st, en}
	}
	return out
}

// Rescale changes parallelism to p2, moving whole buckets of key groups whose
// owner changes. Invalid p2 is rejected without touching any state.
func (s *State) Rescale(p2 int) (Plan, int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := kgrp.CheckP(s.maxP, p2); err != nil {
		return nil, 0, err
	}
	next := s.alloc(p2)
	plan := make(Plan, p2)
	visited := 0
	for i := range plan {
		plan[i].Instance = i
		plan[i].Start, plan[i].End = kgrp.RangeBounds(i, p2, s.maxP)
	}
	for kg := 0; kg < s.maxP; kg++ {
		old := kgrp.Instance(kg, s.p, s.maxP)
		new := kgrp.Instance(kg, p2, s.maxP)
		bucket := s.buckets[old][kg]
		next[new][kg] = bucket // whole bucket reused; no per-key scan
		if old == new {
			continue
		}
		if bucket != nil {
			visited += len(bucket)
		}
		plan[new].Incoming = append(plan[new].Incoming, Move{KeyGroup: kg, From: old})
	}
	s.buckets, s.p, s.lastVisited = next, p2, visited
	return plan, visited, nil
}
