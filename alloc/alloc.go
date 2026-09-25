// Package alloc runs the multi-task recursive water-filling allocation.
// Tasks are organised by ascending demand and the fair share is recomputed
// after each fully satisfied task. It depends only on mf.
package alloc

import (
	"errors"
	"math/big"
	"sort"

	"ontology/mf"
)

// ErrInvalidCapacity is returned when capacity is not positive.
var ErrInvalidCapacity = errors.New("alloc: capacity must be positive")

// Task is one competing job with a finite maximum demand.
type Task struct {
	ID     string
	Demand int64
}

// Allocator keeps tasks in process memory and records the number of tasks
// examined while locating the water level in the most recent Allocate.
type Allocator struct {
	capacity int64
	tasks    []Task
	examined int
}

// New builds an Allocator for total capacity C > 0.
func New(capacity int64) (*Allocator, error) {
	if capacity <= 0 {
		return nil, ErrInvalidCapacity
	}
	return &Allocator{capacity: capacity}, nil
}

// Add registers one task. Callers in the api layer validate input first.
func (a *Allocator) Add(t Task) {
	a.tasks = append(a.tasks, t)
}

// Allocate runs horizontal filling and returns exact shares per task id.
// Zero-demand tasks receive 0 and never enter the filling pool.
func (a *Allocator) Allocate() map[string]mf.Frac {
	out := make(map[string]mf.Frac, len(a.tasks))
	var pos []Task
	for _, t := range a.tasks {
		if t.Demand == 0 {
			out[t.ID] = mf.Int(0)
			continue
		}
		pos = append(pos, t)
	}
	sort.SliceStable(pos, func(i, j int) bool {
		if pos[i].Demand != pos[j].Demand {
			return pos[i].Demand < pos[j].Demand
		}
		return pos[i].ID < pos[j].ID
	})

	m := len(pos)
	used := int64(0)
	s := 0 // number of fully satisfied tasks; also the located water point
	// One ascending pass: each comparison examines exactly one task and we
	// stop at the first task whose demand exceeds the recomputed fair share.
	for s < m {
		fair := mf.Fair(a.capacity-used, m-s)
		if !mf.Full(pos[s].Demand, fair) {
			break
		}
		used += pos[s].Demand
		s++
	}
	if s < m {
		a.examined = s + 1 // s full tasks plus the one task that reveals the level
	} else {
		a.examined = m // unconstrained: every positive-demand task was examined
	}

	for j := 0; j < s; j++ {
		out[pos[j].ID] = mf.Int(pos[j].Demand)
	}
	if s < m {
		level := mf.Make(a.capacity-used, int64(m-s))
		for j := s; j < m; j++ {
			out[pos[j].ID] = level
		}
	}
	return out
}

// SumEquals reports, exactly (big rationals, no float), whether the shares
// sum to target. Used for the conservation invariant.
func SumEquals(shares map[string]mf.Frac, target int64) bool {
	r := new(big.Rat)
	for _, f := range shares {
		r.Add(r, new(big.Rat).SetFrac(big.NewInt(f.N), big.NewInt(f.D)))
	}
	return r.Cmp(new(big.Rat).SetInt64(target)) == 0
}
