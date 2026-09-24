// Package api is the public, concurrency-safe face of the rebalancer.
package api

import (
	"fmt"
	"math/rand"
	"slices"
	"sync"

	"ontology/reb"
)

type State = reb.State

const (
	Unowned   = reb.Unowned
	Consuming = reb.Consuming
	Revoking  = reb.Revoking
)

// Decidable sentinel errors, all distinct.
var (
	ErrBadParam  = reb.ErrBadParam
	ErrDuplicate = reb.ErrDuplicate
	ErrNoMember  = reb.ErrNoMember
	ErrNoRevoke  = reb.ErrNoRevoke
)

// Partition is one row of a Snapshot.
type Partition = reb.Partition

// Group is a consumer group over a fixed number of partitions.
type Group struct {
	mu sync.Mutex
	r  *reb.Rebalancer
}

func New(partitions int) (*Group, error) {
	r, err := reb.New(partitions)
	if err != nil {
		return nil, err
	}
	return &Group{r: r}, nil
}

func (g *Group) call(f func() error) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	return f()
}

// Join adds member x and runs the rebalance rounds.
func (g *Group) Join(x int) error { return g.call(func() error { return g.r.Join(x) }) }

// Leave removes member x; its partitions become unowned at once.
func (g *Group) Leave(x int) error { return g.call(func() error { return g.r.Leave(x) }) }

// RevokeAck confirms all of x's revoking partitions.
func (g *Group) RevokeAck(x int) error { return g.call(func() error { return g.r.RevokeAck(x) }) }

func (g *Group) Owner(p int) (int, State, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.r.Owner(p)
}

// Snapshot returns holder and state of every partition.
func (g *Group) Snapshot() []Partition {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.r.Snapshot()
}

// SelfCheck verifies the four invariants on built-in op sequences
// run against fresh groups; it never touches the receiver's state.
func (g *Group) SelfCheck() error {
	for _, P := range []int{1, 2, 8, 33} {
		for seed := int64(0); seed < 4; seed++ {
			if err := selfRun(P, seed); err != nil {
				return err
			}
		}
	}
	return nil
}

func naiveTarget(p int, mem []int) int {
	best := -1
	for _, m := range mem {
		if m >= p && (best < 0 || m < best) {
			best = m
		}
	}
	if best < 0 {
		best = mem[0] // mem is sorted: wrap to the minimum
	}
	return best
}

// selfRun checks the invariants after every op of a pseudo-random sequence.
func selfRun(P int, seed int64) error {
	rng := rand.New(rand.NewSource(seed*7919 + int64(P)))
	g, _ := New(P)
	var mem []int
	for step := 0; step < 150; step++ {
		x := rng.Intn(P)
		before := g.Snapshot()
		var err error
		switch rng.Intn(3) {
		case 0:
			if err = g.Join(x); err == nil {
				i, _ := slices.BinarySearch(mem, x)
				mem = slices.Insert(mem, i, x)
			}
		case 1:
			if err = g.Leave(x); err == nil {
				if i, ok := slices.BinarySearch(mem, x); ok {
					mem = slices.Delete(mem, i, i+1)
				}
			}
		case 2:
			err = g.RevokeAck(x)
		}
		if err != nil { // invariant 4: rejected ops leave no trace
			if !slices.Equal(before, g.Snapshot()) {
				return fmt.Errorf("inv4: rejected op changed state (P=%d step=%d)", P, step)
			}
			continue
		}
		rev := 0
		snap := g.Snapshot()
		for _, pt := range snap {
			if pt.State == Revoking {
				rev++
			}
		}
		for p, pt := range snap {
			if (pt.State == Unowned) != (pt.Owner == -1) || pt.Owner < -1 || pt.Owner >= P {
				return fmt.Errorf("inv1: p%d owner %d state %v", p, pt.Owner, pt.State)
			}
			if pt.State == Consuming && len(mem) > 0 && pt.Owner != naiveTarget(p, mem) {
				return fmt.Errorf("inv3: p%d holder %d != naive %d", p, pt.Owner, naiveTarget(p, mem))
			}
			if rev == 0 && len(mem) > 0 && pt.State != Consuming {
				return fmt.Errorf("inv2: p%d not consuming at quiescence", p)
			}
		}
	}
	return nil
}
