// Package query runs pruned half-open rectangle range queries over a grid.
package query

import (
	"ontology/cell"
	"ontology/geom"
	"ontology/grid"
)

// Result holds the matched points plus pruning counters (unexported fields
// exposed through accessors so callers cannot forge pruning statistics).
type Result struct {
	Points []geom.Point

	cellsVisited int // cells checked as intersect/contained (pruned ones excluded)
	pointsTested int // points tested one by one (fully-taken leaves add 0)
}

// CellsVisited returns how many cells were actually visited (not pruned).
func (r *Result) CellsVisited() int { return r.cellsVisited }

// PointsTested returns how many points went through individual containment.
func (r *Result) PointsTested() int { return r.pointsTested }

// Range executes a half-open rectangle query [x0,x1)×[y0,y1) atomically under
// the grid's read lock, so a concurrent insert/split cannot expose a moving
// point. Pruning:
//
//	disjoint cell  -> skipped entirely (not counted);
//	fully inside Q -> whole cell taken, zero per-point tests;
//	partial overlap -> leaves test points, inner cells are descended.
func Range(g *grid.Grid, q geom.Rect) *Result {
	g.RLock()
	defer g.RUnlock()
	res := &Result{}
	res.Points = make([]geom.Point, 0)
	walk(g.Root(), q, res)
	return res
}

func walk(c *cell.Cell, q geom.Rect, res *Result) {
	if !c.Bounds.Intersects(q) {
		return
	}
	res.cellsVisited++
	fullyInside := q.ContainsRect(c.Bounds)
	if !c.Split {
		if fullyInside {
			res.Points = append(res.Points, c.Points...) // whole take: 0 tests
			return
		}
		for _, p := range c.Points {
			res.pointsTested++
			if q.Contains(p) {
				res.Points = append(res.Points, p)
			}
		}
		return
	}
	// Inner cell: descend regardless; "whole take" of an inner cell still
	// means taking the whole area of each descendant leaf.
	for _, ch := range c.Children() {
		walk(ch, q, res)
	}
}

// Count runs a query and returns only the matched-point count.
func Count(g *grid.Grid, q geom.Rect) int { return len(Range(g, q).Points) }
