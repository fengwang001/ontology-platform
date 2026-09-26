// Package tri maintains an incremental Delaunay triangulation in memory.
package tri

import (
	"errors"
	"ontology/geo"
	"sync"
)

var (
	ErrDuplicate = errors.New("tri: duplicate point")
	ErrCollinear = errors.New("tri: first three points collinear")
	ErrRange     = errors.New("tri: coordinate out of range")
)

type tri struct {
	v  [3]int // vertex indices, counter-clockwise
	ok bool
}

// T is an incremental Delaunay triangulation. Safe for concurrent reads.
type T struct {
	mu    sync.RWMutex
	pts   []geo.Point
	seen  map[geo.Point]bool
	tris  []tri
	edge  map[[2]int]int   // directed edge -> owning triangle
	grid  map[[2]int][]int // centroid cell -> triangle ids
	preds int              // incircle/containment predicates in last Insert
}

func New() *T {
	return &T{seen: map[geo.Point]bool{}, edge: map[[2]int]int{}, grid: map[[2]int][]int{}}
}

func (t *T) add(a, b, c int) int {
	id := len(t.tris)
	t.tris = append(t.tris, tri{v: [3]int{a, b, c}, ok: true})
	t.edge[[2]int{a, b}] = id
	t.edge[[2]int{b, c}] = id
	t.edge[[2]int{c, a}] = id
	g := [2]int{(t.pts[a].X + t.pts[b].X + t.pts[c].X) / 3 / 512, (t.pts[a].Y + t.pts[b].Y + t.pts[c].Y) / 3 / 512}
	t.grid[g] = append(t.grid[g], id)
	return id
}

func (t *T) del(id int) {
	v := t.tris[id].v
	t.tris[id].ok = false
	delete(t.edge, [2]int{v[0], v[1]})
	delete(t.edge, [2]int{v[1], v[2]})
	delete(t.edge, [2]int{v[2], v[0]})
}
func third(w [3]int, a, b int) int {
	for _, x := range w {
		if x != a && x != b {
			return x
		}
	}
	return -1
}

// legalize flips the edge opposite p=v[s] of triangle i while it is illegal.
func (t *T) legalize(i, s int) {
	v := t.tris[i].v
	p, a, b := v[s], v[(s+1)%3], v[(s+2)%3]
	j, ok := t.edge[[2]int{b, a}]
	if !ok {
		return
	}
	q := third(t.tris[j].v, a, b)
	t.preds++
	if geo.InCircle(t.pts[p], t.pts[a], t.pts[b], t.pts[q]) <= 0 {
		return
	}
	t.del(i)
	t.del(j)
	t.legalize(t.add(p, a, q), 0)
	t.legalize(t.add(p, q, b), 0)
}

// Insert adds one point; rejected inputs leave the state untouched.
func (t *T) Insert(x, y int) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.preds = 0
	if x < -10000 || x > 10000 || y < -10000 || y > 10000 {
		return ErrRange
	}
	p := geo.Point{X: x, Y: y}
	if t.seen[p] {
		return ErrDuplicate
	}
	n := len(t.pts)
	if n == 2 && geo.Orient2D(t.pts[0], t.pts[1], p) == 0 {
		return ErrCollinear
	}
	t.pts = append(t.pts, p)
	t.seen[p] = true
	if n < 2 {
		return nil
	}
	if n == 2 {
		v := [3]int{0, 1, 2}
		if geo.Orient2D(t.pts[0], t.pts[1], p) < 0 {
			v[1], v[2] = 2, 1
		}
		t.add(v[0], v[1], v[2])
		return nil
	}
	if i := t.walk(p, t.near(p)); i >= 0 {
		v := t.tris[i].v
		t.preds++
		if geo.PointInTriangle(p, t.pts[v[0]], t.pts[v[1]], t.pts[v[2]]) {
			t.del(i)
			t.legalize(t.add(n, v[0], v[1]), 0)
			t.legalize(t.add(n, v[1], v[2]), 0)
			t.legalize(t.add(n, v[2], v[0]), 0)
			return nil
		}
		for s := 0; s < 3; s++ { // p may lie strictly on the edge opposite v[s]
			a, b := v[(s+1)%3], v[(s+2)%3]
			if geo.Orient2D(t.pts[a], t.pts[b], p) != 0 || (p.X-t.pts[a].X)*(p.X-t.pts[b].X)+(p.Y-t.pts[a].Y)*(p.Y-t.pts[b].Y) >= 0 {
				continue
			}
			j, hasJ := t.edge[[2]int{b, a}]
			d := third(t.tris[j].v, a, b)
			t.del(i)
			ids := []int{t.add(n, v[s], a), t.add(n, b, v[s])}
			if hasJ {
				t.del(j)
				ids = append(ids, t.add(n, d, b), t.add(n, a, d))
			}
			for _, id := range ids {
				t.legalize(id, 0)
			}
			return nil
		}
	}
	var vis [][2]int // p is outside the hull: fan over visible boundary edges
	for e := range t.edge {
		if _, ok := t.edge[[2]int{e[1], e[0]}]; !ok && geo.Orient2D(t.pts[e[0]], t.pts[e[1]], p) < 0 {
			vis = append(vis, e)
		}
	}
	for _, e := range vis {
		t.legalize(t.add(n, e[1], e[0]), 0)
	}
	return nil
}
