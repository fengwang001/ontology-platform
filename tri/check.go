package tri

import (
	"errors"
	"ontology/geo"
	"reflect"
)

var errCheck = errors.New("tri: self-check failed")

// walk returns the triangle containing p (inside or on an edge), or -1.
func (t *T) walk(p geo.Point, i int) int {
	seen := map[int]bool{}
	for !seen[i] {
		seen[i] = true
		v := t.tris[i].v
		s := 0
		for ; s < 3; s++ {
			if geo.Orient2D(t.pts[v[(s+1)%3]], t.pts[v[(s+2)%3]], p) < 0 {
				break
			}
		}
		if s == 3 {
			return i
		}
		j, ok := t.edge[[2]int{v[(s+2)%3], v[(s+1)%3]}]
		if !ok {
			return -1 // p is outside the hull
		}
		i = j
	}
	for id, tr := range t.tris { // cycle safety: brute scan
		if w := tr.v; tr.ok && geo.Orient2D(t.pts[w[0]], t.pts[w[1]], p) >= 0 && geo.Orient2D(t.pts[w[1]], t.pts[w[2]], p) >= 0 && geo.Orient2D(t.pts[w[2]], t.pts[w[0]], p) >= 0 {
			return id
		}
	}
	return -1
}

func (t *T) near(p geo.Point) int {
	for _, id := range t.grid[[2]int{p.X / 512, p.Y / 512}] {
		if t.tris[id].ok {
			return id
		}
	}
	for id, tr := range t.tris {
		if tr.ok {
			return id
		}
	}
	return -1
}

// Triangles returns all live triangles, each counter-clockwise.
func (t *T) Triangles() [][3]geo.Point {
	t.mu.RLock()
	defer t.mu.RUnlock()
	var out [][3]geo.Point
	for _, tr := range t.tris {
		if tr.ok {
			out = append(out, [3]geo.Point{t.pts[tr.v[0]], t.pts[tr.v[1]], t.pts[tr.v[2]]})
		}
	}
	return out
}

// Hull returns the convex hull vertices counter-clockwise.
func (t *T) Hull() []geo.Point {
	t.mu.RLock()
	defer t.mu.RUnlock()
	next := map[int]int{}
	s := -1
	for e := range t.edge {
		if _, ok := t.edge[[2]int{e[1], e[0]}]; !ok {
			next[e[0]] = e[1]
			s = e[0]
		}
	}
	var out []geo.Point
	for c := s; s >= 0; {
		out = append(out, t.pts[c])
		if c = next[c]; c == s {
			break
		}
	}
	return out
}

// checkTris verifies positive area, edge sharing, count and local Delaunay.
func checkTris(n int, trs [][3]geo.Point) error {
	opp := map[[2]geo.Point]geo.Point{} // directed edge -> opposite vertex
	for _, tr := range trs {
		if geo.Orient2D(tr[0], tr[1], tr[2]) <= 0 {
			return errCheck
		}
		for e := 0; e < 3; e++ {
			a, b := tr[e], tr[(e+1)%3]
			if _, dup := opp[[2]geo.Point{a, b}]; dup {
				return errCheck
			}
			opp[[2]geo.Point{a, b}] = tr[(e+2)%3]
		}
	}
	h := 0
	for e, q := range opp {
		if r, ok := opp[[2]geo.Point{e[1], e[0]}]; !ok {
			h++
		} else if geo.InCircle(e[0], e[1], q, r) > 0 {
			return errCheck
		}
	}
	if len(trs) != 2*n-2-h {
		return errCheck
	}
	return nil
}

var builtin = []geo.Point{{X: 0, Y: 0}, {X: 7, Y: 1}, {X: 3, Y: 8}, {X: 9, Y: 5}, {X: 2, Y: 4},
	{X: 11, Y: 3}, {X: 5, Y: 11}, {X: 1, Y: 7}, {X: 8, Y: 9}, {X: 4, Y: 2}}

// SelfCheck verifies all four invariants on a built-in point sequence.
func SelfCheck() error {
	f := New()
	for _, p := range builtin {
		if err := f.Insert(p.X, p.Y); err != nil {
			return err
		}
	}
	trs := f.Triangles()
	if err := checkTris(len(builtin), trs); err != nil {
		return err
	}
	for _, tr := range trs { // empty-circle property (brute-force definition)
		for _, p := range builtin {
			if p != tr[0] && p != tr[1] && p != tr[2] && geo.InCircle(tr[0], tr[1], tr[2], p) > 0 {
				return errCheck
			}
		}
	}
	if f.Insert(builtin[0].X, builtin[0].Y) != ErrDuplicate || f.Insert(20001, 0) != ErrRange {
		return errCheck
	}
	g := New()
	g.Insert(0, 0)
	g.Insert(1, 1)
	if g.Insert(2, 2) != ErrCollinear || !reflect.DeepEqual(trs, f.Triangles()) {
		return errCheck
	}
	return nil
}
