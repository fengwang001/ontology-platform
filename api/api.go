// Package api is the public facade for EBR; dependency direction is strictly
// one way: api -> retire -> epoch.
package api

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"

	"ontology/epoch"
	"ontology/retire"
)

// Four pairwise-distinct judgeable sentinel errors.
var (
	ErrDuplicateEnter = epoch.ErrDuplicateEnter
	ErrNotActive      = epoch.ErrNotActive
	ErrInvalidNode    = retire.ErrInvalidNode
	ErrAlreadyRetired = retire.ErrAlreadyRetired
)

type EBR struct {
	reg  *epoch.Registry
	list *retire.List
}

func New() *EBR { r := epoch.New(); return &EBR{reg: r, list: retire.New(r)} }

func (e *EBR) Enter(id int) error { return e.reg.Enter(id) }
func (e *EBR) Exit(id int) error  { return e.reg.Exit(id) }
func (e *EBR) Retire(n int) error { return e.list.Retire(n) }
func (e *EBR) AdvanceEpoch()      { e.reg.Advance() }
func (e *EBR) Reclaim() []int     { return e.list.Reclaim() }

// State is an immutable point-in-time view used by tests and SelfCheck.
type State struct {
	G       int64
	Active  map[int]int64
	Retired []retire.Item
}

func (e *EBR) Snapshot() State {
	return State{G: e.reg.G(), Active: e.reg.Active(), Retired: e.list.Pending()}
}

// expectReclaim computes, from a snapshot, exactly the set a naive
// mutex-guarded reference would free: retired nodes whose epoch is below the
// minimum active epoch (all of them when no thread is active).
func expectReclaim(s State) []int {
	min, has := int64(0), false
	for _, e := range s.Active {
		if !has || e < min {
			min, has = e, true
		}
	}
	out := []int{}
	for _, it := range s.Retired {
		if !has || it.Epoch < min {
			out = append(out, it.Node)
		}
	}
	return out
}

// SelfCheck replays built-in sequences on fresh internal managers (the
// receiver is never mutated) and verifies all four invariants.
func (e *EBR) SelfCheck() error {
	x := New()
	type probe struct {
		f    func() error
		want error
	}
	probes := []probe{ // invariant 4: each rejection is judgeable and traceless
		{func() error { return x.Enter(1) }, nil},
		{func() error { return x.Enter(1) }, ErrDuplicateEnter},
		{func() error { return x.Exit(9) }, ErrNotActive},
		{func() error { return x.Retire(0) }, ErrInvalidNode},
		{func() error { return x.Retire(10) }, nil},
		{func() error { return x.Retire(10) }, ErrAlreadyRetired},
	}
	for i, c := range probes {
		before := x.Snapshot()
		if err := c.f(); !errors.Is(err, c.want) {
			return fmt.Errorf("probe %d: %v want %v", i, err, c.want)
		} else if err != nil && !reflect.DeepEqual(before, x.Snapshot()) {
			return fmt.Errorf("probe %d left a trace", i)
		}
	}
	x, rng := New(), rand.New(rand.NewSource(1))
	for it := 0; it < 3000; it++ { // invariants 1 & 2: every Reclaim matches the
		switch rng.Intn(6) { //     naive from-snapshot expectation exactly
		case 0:
			_ = x.Enter(rng.Intn(4))
		case 1:
			_ = x.Exit(rng.Intn(4))
		case 2:
			_ = x.Retire(1 + rng.Intn(8))
		case 3:
			_ = x.Retire(-rng.Intn(3))
		case 4:
			x.AdvanceEpoch()
		case 5:
			want := expectReclaim(x.Snapshot())
			if got := x.Reclaim(); !reflect.DeepEqual(got, want) {
				return fmt.Errorf("iter %d reclaim %v want %v", it, got, want)
			}
		}
	}
	for id := range x.Snapshot().Active { // invariant 3: none leak once all exit
		_ = x.Exit(id)
	}
	if x.Reclaim(); len(x.Snapshot().Retired) != 0 {
		return errors.New("nodes leaked after every thread exited")
	}
	return nil
}
