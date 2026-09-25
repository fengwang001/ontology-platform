// Package gossip maintains a set of nodes holding integers and performs
// pairwise averaging exchanges on them. It depends only on package avg.
package gossip

import (
	"errors"
	"sync"

	"ontology/avg"
)

// Sentinel errors for every rejectable operation; all mutually distinct.
var (
	ErrEmptyID      = errors.New("gossip: empty node id")
	ErrDuplicate    = errors.New("gossip: duplicate node id")
	ErrNotFound     = errors.New("gossip: node not found")
	ErrSelfExchange = errors.New("gossip: exchange node with itself")
)

// Graph is a set of nodes with pairwise averaging exchanges.
// The zero value is not usable; use New.
type Graph struct {
	mu   sync.RWMutex
	vals map[string]int
	sum  int
	// touched records how many nodes the most recent Exchange read and
	// wrote. It is deliberately unexported: no exported API may expose it.
	touched int
}

// New returns an empty Graph.
func New() *Graph {
	return &Graph{vals: make(map[string]int)}
}

// Add inserts a node. It fails (leaving state untouched) on an empty ID
// or a duplicate ID.
func (g *Graph) Add(id string, v int) error {
	if id == "" {
		return ErrEmptyID
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.vals[id]; ok {
		return ErrDuplicate
	}
	g.vals[id] = v
	g.sum += v
	return nil
}

// Exchange averages nodes i and j per the avg.Split rule. All validation
// happens before any write, so a rejected exchange changes nothing.
func (g *Graph) Exchange(i, j string) error {
	if i == j {
		return ErrSelfExchange
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	vi, ok := g.vals[i]
	if !ok {
		return ErrNotFound
	}
	vj, ok := g.vals[j]
	if !ok {
		return ErrNotFound
	}
	ni, nj := avg.Split(i, vi, j, vj)
	g.vals[i], g.vals[j] = ni, nj
	g.touched = 2 // exactly the two participants; never a full scan
	return nil
}

// Value returns the current value of a node.
func (g *Graph) Value(id string) (int, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	v, ok := g.vals[id]
	if !ok {
		return 0, ErrNotFound
	}
	return v, nil
}

// Sum returns the sum of all node values; exchanges conserve it.
func (g *Graph) Sum() int {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.sum
}

// Spread returns the maximum absolute difference between any two node
// values, i.e. max-min. It never increases across exchanges.
func (g *Graph) Spread() int {
	g.mu.RLock()
	defer g.mu.RUnlock()
	if len(g.vals) == 0 {
		return 0
	}
	lo, hi, first := 0, 0, true
	for _, v := range g.vals {
		if first || v < lo {
			lo = v
		}
		if first || v > hi {
			hi = v
		}
		first = false
	}
	return hi - lo
}
