// Package grid owns the root cell, coordinate validation and the mutex
// guarding all concurrent access. Query state lives in the query package.
package grid

import (
	"errors"
	"sync"

	"ontology/cell"
	"ontology/geom"
)

// Default tuning constants.
const (
	DefaultCapacity = 16
	DefaultMaxDepth = 32
)

// DefaultRoot is the default universe: [-1e9,1e9) x [-1e9,1e9).
var DefaultRoot = geom.Rect{-1e9, -1e9, 1e9, 1e9}

var (
	ErrInvalidCoord = errors.New("grid: point contains NaN or Inf")
	ErrOutOfBounds  = errors.New("grid: point outside root bounds")
	ErrNotFound     = errors.New("grid: point id not found")
)

// Stats is the readable summary kept in memory.
type Stats struct {
	Capacity int
	MaxDepth int
	Skipped  uint64
}

// Grid is the concurrency-safe quadtree index.
type Grid struct {
	mu       sync.RWMutex
	root     *cell.Cell
	capacity int
	maxDepth int
	skipped  uint64
	nextID   uint64
}

// New creates a grid over bounds with the given tuning (0 => defaults).
func New(bounds geom.Rect, capacity, maxDepth int) *Grid {
	if capacity <= 0 {
		capacity = DefaultCapacity
	}
	if maxDepth <= 0 {
		maxDepth = DefaultMaxDepth
	}
	return &Grid{root: cell.New(bounds), capacity: capacity, maxDepth: maxDepth}
}

// RLock/RUnlock expose read locking to the query package.
func (g *Grid) RLock()   { g.mu.RLock() }
func (g *Grid) RUnlock() { g.mu.RUnlock() }

// Root returns the root cell (caller must hold RLock).
func (g *Grid) Root() *cell.Cell { return g.root }

// Add inserts p, assigning a fresh ID when p.ID==0. Returns the stored ID.
func (g *Grid) Add(p geom.Point) (uint64, error) {
	if !geom.ValidPoint(p) {
		g.mu.Lock()
		g.skipped++
		g.mu.Unlock()
		return 0, ErrInvalidCoord
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.root.Bounds.Contains(p) {
		g.skipped++
		return 0, ErrOutOfBounds
	}
	g.nextID++
	p.ID = g.nextID
	g.root.Insert(p, g.capacity, g.maxDepth)
	return p.ID, nil
}

// Remove deletes by ID.
func (g *Grid) Remove(id uint64) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.root.RemoveAt(id) {
		return ErrNotFound
	}
	return nil
}

// Count returns the total number of indexed points.
func (g *Grid) Count() int {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.root.Count()
}

// Stats returns a snapshot of the in-memory counters.
func (g *Grid) Stats() Stats {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return Stats{Capacity: g.capacity, MaxDepth: g.maxDepth, Skipped: g.skipped}
}

// Capacity and MaxDepth accessors used by persistence.
func (g *Grid) Capacity() int { return g.capacity }
func (g *Grid) MaxDepth() int { return g.maxDepth }

// RootBounds returns the bounds of the root cell.
func (g *Grid) RootBounds() geom.Rect { return g.root.Bounds }

// Rebuild constructs a grid from an already-built tree (used by persist).
func Rebuild(root *cell.Cell, capacity, maxDepth int, skipped uint64) *Grid {
	if capacity <= 0 {
		capacity = DefaultCapacity
	}
	if maxDepth <= 0 {
		maxDepth = DefaultMaxDepth
	}
	return &Grid{
		root:     root,
		capacity: capacity,
		maxDepth: maxDepth,
		skipped:  skipped,
		nextID:   0,
	}
}
