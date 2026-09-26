// Package tri maintains an incremental Delaunay triangulation in memory.
package tri

import (
	"errors"

	"ontology/geo"
)

var (
	ErrDuplicatePoint = errors.New("tri: duplicate point coordinates")
	ErrCollinearStart = errors.New("tri: first three points must be non-collinear")
)

const cell, suM = 64, 100000 // grid cell size; enclosing super-triangle half-size

type face struct{ v, n [3]int } // v: CCW ids (0-2 virtual, >=3 real); n[k]: neighbor opposite v[k], -1 outer

type Mesh struct {
	pt []geo.Point
	ex map[geo.Point]struct{}
	f  []*face
	gr map[[2]int][]int // spatial grid cell -> face ids by centroid (nil skipped)
	nc int              // unexported: InCircle predicates used by the last Insert
	lf int              // most recently created live face (walk-seed fallback)
}

func New() *Mesh {
	m := &Mesh{ex: map[geo.Point]struct{}{}, gr: map[[2]int][]int{}}
	m.pt = []geo.Point{{X: -suM, Y: -suM}, {X: 3 * suM, Y: -suM}, {X: -suM, Y: 3 * suM}}
	m.add([3]int{0, 1, 2})
	return m
}
func ckey(x, y int) [2]int { // floor-division cell key, valid for negative coords
	fx, fy := x/cell, y/cell
	if x < 0 && x%cell != 0 {
		fx--
	}
	if y < 0 && y%cell != 0 {
		fy--
	}
	return [2]int{fx, fy}
}
func ekey(u, v int) [2]int {
	if u > v {
		u, v = v, u
	}
	return [2]int{u, v}
}
func (m *Mesh) add(v [3]int) int {
	fi, p := len(m.f), m.pt
	m.f, m.lf = append(m.f, &face{v: v, n: [3]int{-1, -1, -1}}), fi
	c := ckey((p[v[0]].X+p[v[1]].X+p[v[2]].X)/3, (p[v[0]].Y+p[v[1]].Y+p[v[2]].Y)/3)
	m.gr[c] = append(m.gr[c], fi)
	return fi
}
func (m *Mesh) in(fi int, q geo.Point) int {
	m.nc++
	g := m.f[fi]
	return geo.InCircle(m.pt[g.v[0]], m.pt[g.v[1]], m.pt[g.v[2]], q)
}
func (m *Mesh) Insert(q geo.Point) error {
	if _, ok := m.ex[q]; ok {
		return ErrDuplicatePoint
	}
	if len(m.pt) == 5 && geo.Orient2D(m.pt[3], m.pt[4], q) == 0 {
		return ErrCollinearStart
	}
	pid := len(m.pt)
	m.pt, m.ex[q], m.nc = append(m.pt, q), struct{}{}, 0
	f0 := m.locate(q)
	bad, qb := map[int]bool{f0: true}, []int{f0}
	for len(qb) > 0 { // cavity BFS; cross an edge only when strictly inside; ==0 keeps it
		g := m.f[qb[0]]
		qb = qb[1:]
		for k := 0; k < 3; k++ { // a point on a shared edge is inside BOTH circles
			if nb := g.n[k]; nb != -1 && !bad[nb] && m.in(nb, q) > 0 {
				bad[nb], qb = true, append(qb, nb)
			}
		}
	}
	type bd struct{ a, b, o int } // cavity boundary edge + retained neighbor
	fan := []bd{}
	for fi := range bad {
		g := m.f[fi]
		for k := 0; k < 3; k++ { // cavity boundary edges seed the reconnection fan
			if nb := g.n[k]; nb == -1 || !bad[nb] {
				fan = append(fan, bd{g.v[(k+1)%3], g.v[(k+2)%3], nb})
			}
		}
	}
	rad := map[[2]int][]int{} // radial edge -> new-face slots (fi<<2|slot)
	for _, e := range fan {
		v := [3]int{pid, e.a, e.b}
		if geo.Orient2D(q, m.pt[e.a], m.pt[e.b]) < 0 {
			v[1], v[2] = v[2], v[1]
		}
		nf := m.add(v)
		m.f[nf].n[0] = e.o
		if e.o != -1 { // re-point the retained neighbor across the fan edge
			h := m.f[e.o]
			for k := 0; k < 3; k++ {
				if ekey(h.v[(k+1)%3], h.v[(k+2)%3]) == ekey(e.a, e.b) {
					h.n[k] = nf
				}
			}
		}
		rad[ekey(pid, v[1])] = append(rad[ekey(pid, v[1])], nf<<2|2)
		rad[ekey(pid, v[2])] = append(rad[ekey(pid, v[2])], nf<<2|1)
	}
	for _, s := range rad { // pair radial slots; a lone slot is a hull edge
		if len(s) == 2 {
			m.f[s[0]>>2].n[s[0]&3], m.f[s[1]>>2].n[s[1]&3] = s[1]>>2, s[0]>>2
		}
	}
	for fi := range bad {
		m.f[fi] = nil
	}
	return nil
}
func (m *Mesh) locate(q geo.Point) int {
	f := m.lf // grid seed: a live face registered in q's cell; else latest face
	for _, fi := range m.gr[ckey(q.X, q.Y)] {
		if m.f[fi] != nil {
			f = fi
		}
	}
	for steps := 4*len(m.pt) + 32; steps >= 0; steps-- { // straight visibility walk
		g, mv := m.f[f], -1
		for k := 0; k < 3; k++ {
			if geo.Orient2D(m.pt[g.v[(k+1)%3]], m.pt[g.v[(k+2)%3]], q) < 0 {
				mv = k
				break
			}
		}
		if mv < 0 || g.n[mv] < 0 {
			return f
		}
		f = g.n[mv]
	}
	return f
}
func (m *Mesh) Triangles() (out [][3]geo.Point) { // each triangle is CCW
	for _, g := range m.f {
		if g != nil && g.v[0] >= 3 && g.v[1] >= 3 && g.v[2] >= 3 {
			out = append(out, [3]geo.Point{m.pt[g.v[0]], m.pt[g.v[1]], m.pt[g.v[2]]})
		}
	}
	return
}
