// Package api is the public façade: api -> rescale -> kgrp only.
package api

import (
	"errors"
	"fmt"

	"ontology/kgrp"
	"ontology/rescale"
)

// Distinct sentinel errors for the three rejection causes.
var (
	ErrInvalidMaxP        = kgrp.ErrInvalidMaxP
	ErrInvalidParallelism = kgrp.ErrInvalidParallelism
	ErrEmptyKey           = kgrp.ErrEmptyKey
)

type Plan = rescale.Plan
type State struct{ st *rescale.State }

// New creates state with maximum parallelism maxP and current parallelism p.
func New(maxP, p int) (*State, error) {
	st, err := rescale.New(maxP, p)
	if err != nil {
		return nil, err
	}
	return &State{st: st}, nil
}

func (s *State) Put(key string, val int64) error { return s.st.Put(key, val) }
func (s *State) Get(key string) (int64, bool)    { return s.st.Get(key) }
func (s *State) Owner(key string) int            { return s.st.Owner(key) }
func (s *State) Ranges() [][2]int                { return s.st.Ranges() }

// Rescale changes parallelism and returns the migration plan.
func (s *State) Rescale(p2 int) (Plan, error) {
	plan, _, err := s.st.Rescale(p2)
	return plan, err
}

// SelfCheck verifies the four invariants on a built-in data set.
func (s *State) SelfCheck() error {
	const maxP, p1, p2 = 10, 3, 4
	st, err := New(maxP, p1)
	if err != nil {
		return err
	}
	keys := make([]string, 0, 40)
	vals := make(map[string]int64, 40)
	for n := 1; n <= 40; n++ {
		k := fmt.Sprintf("check-key-%d", n)
		keys, vals[k] = append(keys, k), int64(n)
		if err := st.Put(k, vals[k]); err != nil {
			return err
		}
	}
	if err := checkNaive(st, keys, vals, p1); err != nil {
		return err
	}
	if err := checkPartition(maxP, p1); err != nil {
		return err
	}
	before := fmt.Sprint(st.Ranges())
	cases := []struct {
		err  error
		call func() error
	}{
		{ErrInvalidParallelism, func() error { _, e := st.Rescale(0); return e }},
		{ErrInvalidParallelism, func() error { _, e := st.Rescale(maxP + 1); return e }},
		{ErrEmptyKey, func() error { return st.Put("", 1) }},
		{ErrInvalidMaxP, func() error { _, e := New(0, 1); return e }},
	}
	for _, c := range cases {
		if !errors.Is(c.call(), c.err) {
			return fmt.Errorf("SelfCheck: want %v", c.err)
		}
	}
	if fmt.Sprint(st.Ranges()) != before {
		return errors.New("SelfCheck: rejected operation mutated ranges")
	}
	wantMoved := 0
	for _, k := range keys {
		kg := int(kgrp.Hash(k) % uint32(maxP))
		if kgrp.Instance(kg, p1, maxP) != kgrp.Instance(kg, p2, maxP) {
			wantMoved++
		}
	}
	if _, gotMoved, err := st.st.Rescale(p2); err != nil || gotMoved != wantMoved {
		if err != nil {
			return err
		}
		return fmt.Errorf("SelfCheck: moved=%d want %d", gotMoved, wantMoved)
	}
	if err := checkNaive(st, keys, vals, p2); err != nil {
		return err
	}
	return checkPartition(maxP, p2)
}

// checkNaive verifies invariant 1 against the naive per-key-group formula.
func checkNaive(st *State, keys []string, vals map[string]int64, p int) error {
	maxP := st.st.MaxP()
	ranges := st.Ranges()
	for kg := 0; kg < maxP; kg++ {
		owner := -1
		for i, r := range ranges {
			if kg >= r[0] && kg < r[1] {
				owner = i
				break
			}
		}
		if owner != kgrp.Instance(kg, p, maxP) {
			return fmt.Errorf("naive mismatch kg=%d owner=%d", kg, owner)
		}
	}
	for _, k := range keys {
		kg := int(kgrp.Hash(k) % uint32(maxP))
		if st.Owner(k) != kgrp.Instance(kg, p, maxP) {
			return fmt.Errorf("naive mismatch key=%q", k)
		}
		if v, ok := st.Get(k); !ok || v != vals[k] {
			return fmt.Errorf("naive mismatch value of %q", k)
		}
	}
	return nil
}

// checkPartition verifies invariant 2: contiguous partition, length spread ≤1.
func checkPartition(maxP, p int) error {
	st, err := New(maxP, p)
	if err != nil {
		return err
	}
	ranges := st.Ranges()
	if ranges[0][0] != 0 || ranges[p-1][1] != maxP {
		return errors.New("partition: endpoints do not cover [0,maxP)")
	}
	base := ranges[0][1] - ranges[0][0]
	for i := 1; i < len(ranges); i++ {
		r, prev := ranges[i], ranges[i-1]
		if r[0] != prev[1] || r[1] <= r[0] {
			return fmt.Errorf("partition: gap/overlap at %d", i)
		}
		if d := (r[1] - r[0]) - base; d > 1 || d < -1 {
			return fmt.Errorf("partition: length spread >1 at %d", i)
		}
	}
	return nil
}
