// Package grid owns the whole split-tree index: locating cells by coordinate,
// inserting/deleting points, overflow-driven splitting and concurrency.
package grid

import (
	"errors"
	"math"
	"sync"

	"ontology/cell"
	"ontology/geom"
)

// Errors returned for rejected coordinates.
var (
	ErrNaN   = errors.New("grid: NaN coordinate rejected")
	ErrInf   = errors.New("grid: infinite coordinate rejected")
	ErrRange = errors.New("grid: point outside grid bounds")
)

// DefaultBounds covers the whole useful finite plane.
var DefaultBounds = geom.Rect{X0: -1e12, Y0: -1e12, X1: 1e12, Y1: 1e12}

// Grid is a concurrency-safe quadtree index kept in memory.
type Grid struct {
	mu         sync.RWMutex
	root       *cell.Cell
	capacity   int
	nextID     uint64
	skipNaN    int
	skipInf    int
	rejected   int
	totalCells int
}

// New creates an empty grid with the given leaf capacity over bounds b.
func New(b geom.Rect, capacity int) *Grid {
	if capacity < 1 {
		capacity = 1
	}
	return &Grid{root: cell.New(b), capacity: capacity, totalCells: 1}
}

// Cap reports the configured per-leaf capacity.
func (g *Grid) Cap() int { return g.capacity }

// RootBounds returns the covered rectangle.
func (g *Grid) RootBounds() geom.Rect { return g.root.Bounds }

// Stats returns point count, total cell count and rejected-coordinate counters.
func (g *Grid) Stats() (points, cells, skippedNaN, skippedInf, rejected int) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.root.Count(), g.totalCells, g.skipNaN, g.skipInf, g.rejected
}

// Root returns the root cell; callers must hold nothing — the grid exposes it
// for read-only traversal while the query package drives RLock/Lock.
func (g *Grid) Root() *cell.Cell { return g.root }

// RLock/Lock helpers let the query package run traversals atomically.
func (g *Grid) RLock() { g.mu.RLock() }

// RUnlock releases a read lock.
func (g *Grid) RUnlock() { g.mu.RUnlock() }

// Lock acquires the write lock.
func (g *Grid) Lock() { g.mu.Lock() }

// Unlock releases the write lock.
func (g *Grid) Unlock() {
	g.mu.Unlock()
}

// classify validates coordinates and distinguishes NaN from Inf for counters.
func (g *Grid) classify(x, y float64) error {
	if isNaN(x) || isNaN(y) {
		g.skipNaN++
		g.rejected++
		return ErrNaN
	}
	if isInf(x) || isInf(y) {
		g.skipInf++
		g.rejected++
		return ErrInf
	}
	return nil
}

func isNaN(v float64) bool { return math.IsNaN(v) }
func isInf(v float64) bool { return math.IsInf(v, 0) }

// locate walks to the leaf that owns (x,y). Caller holds g.mu.
func (g *Grid) locate(x, y float64) (*cell.Cell, error) {
	b := g.root.Bounds
	if x < b.X0 || x >= b.X1 || y < b.Y0 || y >= b.Y1 {
		return nil, ErrRange
	}
	c := g.root
	for c.Split {
		c = c.Quadrant(x, y)
	}
	return c, nil
}

// Insert validates and adds one point, splitting overflowing leaves (and, when
// a child is immediately over capacity, that child) until the point rests in a
// non-over-capacity leaf or a saturated leaf. It returns the assigned ID.
func (g *Grid) Insert(x, y float64) (uint64, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if err := g.classify(x, y); err != nil {
		return 0, err
	}
	leaf, err := g.locate(x, y)
	if err != nil {
		g.rejected++
		return 0, err
	}
	g.nextID++
	id := g.nextID
	leaf.Points = append(leaf.Points, geom.Point{ID: id, X: x, Y: y})
	for leaf.TrySplit(g.capacity) {
		g.totalCells += 4
		leaf = leaf.Quadrant(x, y)
	}
	return id, nil
}

// Delete removes the point with the given ID. It reports whether it existed.
// Subtrees are never merged (see DESIGN.md §3).
func (g *Grid) Delete(id uint64) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.remove(g.root, id)
}

func (g *Grid) remove(c *cell.Cell, id uint64) bool {
	if c.Split {
		for _, ch := range c.Children() {
			if g.remove(ch, id) {
				return true
			}
		}
		return false
	}
	for i, p := range c.Points {
		if p.ID == id {
			c.Points = append(c.Points[:i], c.Points[i+1:]...)
			return true
		}
	}
	return false
}

// Find reports whether a point ID is present and returns its coordinates.
func (g *Grid) Find(id uint64) (geom.Point, bool) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.find(g.root, id)
}

func (g *Grid) find(c *cell.Cell, id uint64) (geom.Point, bool) {
	if !c.Split {
		for _, p := range c.Points {
			if p.ID == id {
				return p, true
			}
		}
		return geom.Point{}, false
	}
	for _, ch := range c.Children() {
		if p, ok := g.find(ch, id); ok {
			return p, true
		}
	}
	return geom.Point{}, false
}

// Rebuild replaces the in-memory tree (used by persist on load).
func (g *Grid) Rebuild(root *cell.Cell, cells int, nextID uint64) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.root = root
	g.totalCells = cells
	g.nextID = nextID
}
