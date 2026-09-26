// Package crel classifies the relation between a circle and a simple polygon.
package crel

import (
	"fmt"
	"sync/atomic"

	"ontology/cgeom"
)

// Relation is one of the four mutually exclusive circle–polygon relations.
type Relation int

const (
	Crossing  Relation = iota // circle boundary cuts the polygon boundary
	Tangent                   // circle boundary touches the polygon boundary
	Contained                 // circle lies wholly inside the polygon
	Disjoint                  // circle lies wholly outside the polygon
)

func (r Relation) String() string {
	return [...]string{"CROSSING", "TANGENT", "CONTAINED", "DISJOINT"}[r]
}

// Edge is one polygon side.
type Edge struct{ A, B cgeom.Point }
type cellKey struct{ x, y int }

const cellSize = 64

// Engine holds polygon edges partitioned into a spatial grid. It is immutable
// after construction and safe for concurrent read-only use.
type Engine struct {
	poly                                   []cgeom.Point
	edges                                  []Edge
	buckets                                map[cellKey][]int
	minCellX, maxCellX, minCellY, maxCellY int
	// checked counts edges whose distance the most recent MinDist2 actually
	// evaluated. Unexported; its value never crosses the package boundary.
	checked atomic.Int64
}

func cellOf(v int) int {
	q := v / cellSize
	if v < 0 && v%cellSize != 0 {
		q--
	}
	return q
}
func NewEngine(poly []cgeom.Point, edges []Edge) *Engine {
	e := &Engine{poly: poly, edges: edges, buckets: map[cellKey][]int{}}
	e.minCellX, e.minCellY = 1<<30, 1<<30
	e.maxCellX, e.maxCellY = -1<<30, -1<<30
	for i, ed := range edges {
		x0, x1 := cellOf(min(ed.A.X, ed.B.X)), cellOf(max(ed.A.X, ed.B.X))
		y0, y1 := cellOf(min(ed.A.Y, ed.B.Y)), cellOf(max(ed.A.Y, ed.B.Y))
		for cx := x0; cx <= x1; cx++ {
			for cy := y0; cy <= y1; cy++ {
				k := cellKey{x: cx, y: cy}
				e.buckets[k] = append(e.buckets[k], i)
			}
		}
		e.minCellX, e.maxCellX = min(e.minCellX, x0), max(e.maxCellX, x1)
		e.minCellY, e.maxCellY = min(e.minCellY, y0), max(e.maxCellY, y1)
	}
	return e
}
func gap2(p cgeom.Point, pcx, pcy, ring int) int64 {
	// Lower bound on distance to an edge outside the searched cell rectangle.
	loX, hiX := (pcx-ring)*cellSize, (pcx+ring+1)*cellSize
	loY, hiY := (pcy-ring)*cellSize, (pcy+ring+1)*cellSize
	if p.X < loX || p.X >= hiX || p.Y < loY || p.Y >= hiY {
		return 0
	}
	g := int64(min(p.X-loX, hiX-p.X, p.Y-loY, hiY-p.Y))
	return g * g
}
func (e *Engine) MinDist2(p cgeom.Point) cgeom.Rat2 {
	// Exact min segment distance; expand rings until unseen edges can't be closer.
	pcx, pcy := cellOf(p.X), cellOf(p.Y)
	seen := map[int]struct{}{}
	best := cgeom.Rat2{Num: 1 << 62, Den: 1}
	eval := func(i int) {
		if _, ok := seen[i]; ok {
			return
		}
		seen[i] = struct{}{}
		if d := cgeom.PointSegDist2(p, e.edges[i].A, e.edges[i].B); cgeom.CmpRat2(d, best) < 0 {
			best = d
		}
	}
	for ring := 0; ; ring++ {
		cx0, cx1, cy0, cy1 := pcx-ring, pcx+ring, pcy-ring, pcy+ring
		for cx := cx0; cx <= cx1; cx++ { // only the new ring perimeter
			for cy := cy0; cy <= cy1; cy++ {
				if cx != cx0 && cx != cx1 && cy != cy0 && cy != cy1 {
					continue
				}
				for _, i := range e.buckets[cellKey{x: cx, y: cy}] {
					eval(i)
				}
			}
		}
		if cgeom.CmpRat2Int(best, gap2(p, pcx, pcy, ring)) <= 0 {
			break // unseen edges are provably no closer
		}
		if cx0 <= e.minCellX && cx1 >= e.maxCellX && cy0 <= e.minCellY && cy1 >= e.maxCellY {
			break
		}
	}
	e.checked.Store(int64(len(seen)))
	return best
}
func (e *Engine) Inside(p cgeom.Point) bool { return cgeom.PointInPoly(p, e.poly) }
func (e *Engine) Classify(p cgeom.Point, r2 int64) Relation {
	// Four-state rule; equality with r^2 is checked first (always Tangent).
	switch cgeom.CmpRat2Int(e.MinDist2(p), r2) {
	case 0:
		return Tangent
	case -1:
		return Crossing
	default:
		if e.Inside(p) {
			return Contained
		}
		return Disjoint
	}
}
func GridScalingSelfCheck() error {
	// Corner queries over growing grids must touch a constant edge count;
	// returns only ok/error so the counter value is never exposed.
	p := cgeom.Point{X: 5, Y: 5}
	var first int64 = -1
	for _, mk := range [][2]int{{100, 10}, {400, 20}, {1000, 32}, {2500, 50}, {6400, 80}, {10000, 100}} {
		m, k := mk[0], mk[1]
		eds := make([]Edge, 0, m)
		for i := 0; i < m; i++ {
			x, y := (i%k)*256, (i/k)*256
			eds = append(eds, Edge{A: cgeom.Point{X: x, Y: y}, B: cgeom.Point{X: x + 10, Y: y}})
		}
		e := NewEngine(nil, eds)
		e.MinDist2(p)
		n := e.checked.Load()
		if n > 16 || (first >= 0 && n > first) {
			return fmt.Errorf("grid scan grew: m=%d checked=%d first=%d", m, n, first)
		}
		first = n
	}
	return nil
}
