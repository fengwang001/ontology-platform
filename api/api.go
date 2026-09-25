// Package api is the public facade of the gossip averaging aggregator.
// It depends on package gossip (and reuses avg only to step a naive
// reference inside SelfCheck).
package api

import (
	"fmt"

	"ontology/avg"
	"ontology/gossip"
)

// Re-exported sentinel errors; all four are mutually distinct.
var (
	ErrEmptyID      = gossip.ErrEmptyID
	ErrDuplicate    = gossip.ErrDuplicate
	ErrNotFound     = gossip.ErrNotFound
	ErrSelfExchange = gossip.ErrSelfExchange
)

// Agg is a gossip averaging aggregator. Use New.
type Agg struct {
	g *gossip.Graph
}

// New returns an empty aggregator.
func New() *Agg {
	return &Agg{g: gossip.New()}
}

// Add inserts a node; fails on empty or duplicate ID.
func (a *Agg) Add(id string, v int) error { return a.g.Add(id, v) }

// Exchange averages nodes i and j; rejected operations change nothing.
func (a *Agg) Exchange(i, j string) error { return a.g.Exchange(i, j) }

// Value returns the current value of a node.
func (a *Agg) Value(id string) (int, error) { return a.g.Value(id) }

// Sum returns the invariant total of all node values.
func (a *Agg) Sum() int { return a.g.Sum() }

// Spread returns the current maximum pairwise difference.
func (a *Agg) Spread() int { return a.g.Spread() }

// SelfCheck verifies the four invariants on a built-in scenario:
// nodes A=0, B=5, C=7 and the six-exchange sequence from NOTES.md.
// It runs on a private graph and never touches the receiver's state.
func (a *Agg) SelfCheck() error {
	g := gossip.New()
	ref := map[string]int{"A": 0, "B": 5, "C": 7}
	for id, v := range ref {
		if err := g.Add(id, v); err != nil {
			return err
		}
	}
	seq := [][2]string{{"A", "B"}, {"B", "C"}, {"A", "C"}, {"A", "B"}, {"B", "C"}, {"A", "C"}}
	prevSpread := g.Spread()
	for _, p := range seq {
		if err := g.Exchange(p[0], p[1]); err != nil {
			return err
		}
		// naive reference stepped by hand through the same rule
		x, y := avg.Split(p[0], ref[p[0]], p[1], ref[p[1]])
		ref[p[0]], ref[p[1]] = x, y
		if s := g.Sum(); s != 12 { // invariant 1: sum conserved
			return fmt.Errorf("selfcheck: sum drifted to %d", s)
		}
		if s := g.Spread(); s > prevSpread { // invariant 2: spread non-increasing
			return fmt.Errorf("selfcheck: spread rose to %d", s)
		} else {
			prevSpread = s
		}
		for id, want := range ref { // invariant 3: matches naive reference
			if got, err := g.Value(id); err != nil || got != want {
				return fmt.Errorf("selfcheck: %s=%d want %d", id, got, want)
			}
		}
	}
	// invariant 4: rejected operations leave no trace
	before := g.Sum()
	for _, op := range []func() error{
		func() error { return g.Exchange("A", "A") },
		func() error { return g.Exchange("A", "ghost") },
		func() error { return g.Add("A", 1) },
		func() error { return g.Add("", 1) },
	} {
		if op() == nil {
			return fmt.Errorf("selfcheck: invalid operation accepted")
		}
	}
	if g.Sum() != before {
		return fmt.Errorf("selfcheck: rejected operation changed state")
	}
	return nil
}
