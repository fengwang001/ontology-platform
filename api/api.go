// Package api is the public entry point to the incremental Delaunay service.
package api

import (
	"errors"
	"reflect"
	"sync"

	"ontology/geo"
	"ontology/tri"
)

type Point = geo.Point

const MaxCoord = 10000 // inclusive per-axis coordinate bound
var (
	ErrDuplicatePoint = tri.ErrDuplicatePoint
	ErrCollinearStart = tri.ErrCollinearStart
	ErrOutOfBounds    = errors.New("api: coordinates out of bounds (|x|,|y| <= 10000)")
)

type Service struct {
	mu sync.RWMutex
	m  *tri.Mesh
}

func New() (*Service, error) { return &Service{m: tri.New()}, nil }
func norm(a, b Point) [2]Point {
	if b.X < a.X || b.X == a.X && b.Y < a.Y {
		a, b = b, a
	}
	return [2]Point{a, b}
}

func (s *Service) Insert(x, y int) error {
	if x > MaxCoord || x < -MaxCoord || y > MaxCoord || y < -MaxCoord {
		return ErrOutOfBounds
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.m.Insert(geo.Point{X: x, Y: y})
}
func (s *Service) Triangles() [][3]Point {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.m.Triangles()
}

func (s *Service) Hull() []Point {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return hullFrom(s.m.Triangles())
}

func (s *Service) SelfCheck() error {
	if err := checkTriangles(s.Triangles()); err != nil {
		return err
	}
	seq := []Point{{X: 0, Y: 0}, {X: 4, Y: 0}, {X: 0, Y: 4}, {X: 4, Y: 4}, {X: 3, Y: 3}, {X: 2, Y: 1}}
	b, _ := New()
	for _, p := range seq {
		if b.Insert(p.X, p.Y) != nil || checkTriangles(b.Triangles()) != nil { // each step must be valid
			return errors.New("api: built-in six-step sequence failed")
		}
	}
	bad := [][2]any{{Point{X: 3, Y: 3}, ErrDuplicatePoint}, {Point{X: 10001, Y: 0}, ErrOutOfBounds}, {Point{X: 0, Y: -10001}, ErrOutOfBounds}}
	for _, c0 := range bad { // refusals are sentinel-safe and leave state intact
		cp, want := c0[0].(Point), c0[1].(error)
		before := b.Triangles()
		if !errors.Is(b.Insert(cp.X, cp.Y), want) || !reflect.DeepEqual(before, b.Triangles()) {
			return errors.New("api: rejection not sentinel-safe")
		}
	}
	if b.Insert(5, -2) != nil || checkTriangles(b.Triangles()) != nil {
		return errors.New("api: unusable after refusals")
	}
	c, _ := New()
	if c.Insert(0, 0) != nil || c.Insert(1, 1) != nil {
		return errors.New("api: unexpected setup error")
	}
	if !errors.Is(c.Insert(2, 2), ErrCollinearStart) || len(c.Triangles()) != 0 {
		return errors.New("api: collinear start mishandled")
	}
	if c.Insert(0, 1) != nil || checkTriangles(c.Triangles()) != nil {
		return errors.New("api: unusable after collinear refusal")
	}
	return nil
}

func checkTriangles(tris [][3]Point) error {
	pts, edge := map[Point]struct{}{}, map[[2]Point]int{}
	for _, t := range tris {
		pts[t[0]], pts[t[1]], pts[t[2]] = struct{}{}, struct{}{}, struct{}{}
	}
	for _, t := range tris {
		if geo.Orient2D(t[0], t[1], t[2]) <= 0 {
			return errors.New("api: non-CCW or degenerate triangle")
		}
		for q := range pts {
			if q != t[0] && q != t[1] && q != t[2] && geo.InCircle(t[0], t[1], t[2], q) > 0 {
				return errors.New("api: circumcircle strictly contains a point")
			}
		}
		for k := 0; k < 3; k++ {
			edge[norm(t[(k+1)%3], t[(k+2)%3])]++
		}
	}
	h := 0
	for _, n := range edge {
		if n > 2 {
			return errors.New("api: edge shared by more than two triangles")
		}
		if n == 1 {
			h++
		}
	}
	if len(pts) >= 3 && len(tris) != 2*len(pts)-2-h {
		return errors.New("api: triangle count formula violated")
	}
	return nil
}

func hullFrom(tris [][3]Point) []Point {
	cnt := map[[2]Point]int{}
	for _, t := range tris {
		for k := 0; k < 3; k++ {
			cnt[norm(t[(k+1)%3], t[(k+2)%3])]++
		}
	}
	nx, start, first := map[Point]Point{}, Point{}, true
	for _, t := range tris { // only boundary directed edges link the CCW chain
		for k := 0; k < 3; k++ {
			a, b := t[(k+1)%3], t[(k+2)%3]
			if cnt[norm(a, b)] == 1 {
				nx[a] = b // interior stays on the left of a->b
				if first || a.X < start.X || a.X == start.X && a.Y < start.Y {
					start, first = a, false
				}
			}
		}
	}
	if first {
		return nil
	}
	out, seen := []Point{}, map[Point]bool{}
	for v, ok := start, true; ok && !seen[v]; v, ok = nx[v] {
		seen[v], out = true, append(out, v)
	}
	return out
}
