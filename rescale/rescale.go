// Package rescale stores state bucketed by key group and moves only the
// buckets whose owner instance changes on rescale. Depends on kgrp.
package rescale

import "ontology/kgrp"

type Move struct{ KeyGroup, From int } // one migrating key group

type InstancePlan struct {
	Start, End int    // [Start,End) is the new key-group interval
	Incoming   []Move // migrating groups, ascending by KeyGroup
}

type Plan struct {
	FromP, ToP, MovedKeys int
	Instances             []InstancePlan
}

// Store partitions {key -> val} by instance then key group.
type Store struct {
	maxP, p     int
	inst        []map[int]map[string]int64 // inst[i][kg]; empty buckets omitted
	lastVisited int                        // entries visited in last Rescale; group scans excluded; unexported
}

// New creates a Store at parallelism p.
func New(maxP, p int) (*Store, error) {
	if !kgrp.ValidMaxP(maxP) {
		return nil, kgrp.ErrMaxPInvalid
	}
	if !kgrp.ValidP(p, maxP) {
		return nil, kgrp.ErrPInvalid
	}
	return &Store{maxP: maxP, p: p, inst: makeInst(p)}, nil
}
func makeInst(p int) []map[int]map[string]int64 {
	m := make([]map[int]map[string]int64, p)
	for i := range m {
		m[i] = map[int]map[string]int64{}
	}
	return m
}

func (s *Store) P() int { return s.p }
func (s *Store) Ranges() [][2]int {
	r, _ := kgrp.Ranges(s.p, s.maxP)
	return r
}

func (s *Store) locate(key string) (int, int, error) {
	kg, e := kgrp.KeyGroup(key, s.maxP)
	return kg, kgrp.Owner(kg, s.p, s.maxP), e
}

// Put sets key=val; an empty key is rejected before any state change.
func (s *Store) Put(key string, val int64) error {
	kg, o, e := s.locate(key)
	if e != nil {
		return e
	}
	b := s.inst[o][kg]
	if b == nil {
		b = map[string]int64{}
		s.inst[o][kg] = b
	}
	b[key] = val
	return nil
}
func (s *Store) Get(key string) (int64, bool) {
	kg, o, e := s.locate(key)
	if e != nil {
		return 0, false
	}
	v, ok := s.inst[o][kg][key]
	return v, ok
}
func (s *Store) Owner(key string) (int, error) {
	_, o, e := s.locate(key)
	return o, e
}

// Rescale moves to p2. Unchanged buckets keep their map identity; moved
// buckets are copied once. Built in locals and committed last, so an illegal
// p2 leaves the store untouched.
func (s *Store) Rescale(p2 int) (Plan, error) {
	if !kgrp.ValidP(p2, s.maxP) {
		return Plan{}, kgrp.ErrPInvalid
	}
	p1 := s.p
	ranges, _ := kgrp.Ranges(p2, s.maxP)
	plan := Plan{FromP: p1, ToP: p2, Instances: make([]InstancePlan, p2)}
	next, visited := makeInst(p2), 0
	for i := 0; i < p2; i++ {
		plan.Instances[i] = InstancePlan{Start: ranges[i][0], End: ranges[i][1]}
	}
	for kg := 0; kg < s.maxP; kg++ { // group scan itself is not counted
		old, newO := kgrp.Owner(kg, p1, s.maxP), kgrp.Owner(kg, p2, s.maxP)
		b := s.inst[old][kg]
		if old == newO {
			next[newO][kg] = b // whole bucket re-hung, zero entries visited
			continue
		}
		plan.Instances[newO].Incoming = append(plan.Instances[newO].Incoming, Move{kg, old})
		plan.MovedKeys += len(b)
		if b != nil {
			nb := make(map[string]int64, len(b))
			for k, v := range b { // only moved buckets' entries are visited
				nb[k], visited = v, visited+1
			}
			next[newO][kg] = nb
		}
	}
	s.inst, s.p, s.lastVisited = next, p2, visited
	return plan, nil
}

// Check verifies invariants 1-2: ranges equal the per-group formula merge,
// intervals partition [0,maxP), lengths differ by at most 1, and every key
// physically sits in its formula owner's bucket.
func (s *Store) Check() error {
	r := s.Ranges()
	for i, iv := range r {
		s0, e0 := kgrp.Range(i, s.p, s.maxP)
		if iv != [2]int{s0, e0} || iv[0] >= iv[1] {
			return kgrp.ErrPInvalid
		}
		if i > 0 {
			d := (iv[1] - iv[0]) - (r[i-1][1] - r[i-1][0])
			if iv[0] != r[i-1][1] || d > 1 || d < -1 {
				return kgrp.ErrPInvalid
			}
		}
	}
	if r[0][0] != 0 || r[len(r)-1][1] != s.maxP {
		return kgrp.ErrPInvalid
	}
	for i, groups := range s.inst {
		for kg, b := range groups {
			if kgrp.Owner(kg, s.p, s.maxP) != i {
				return kgrp.ErrPInvalid
			}
			for key := range b {
				if g, e := kgrp.KeyGroup(key, s.maxP); e != nil || g != kg {
					return kgrp.ErrPInvalid
				}
			}
		}
	}
	return nil
}
