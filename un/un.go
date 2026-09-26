// Package un 计算两个简单多边形的并集边界，依赖 poly。并集边界 = 双方不在对方严格内部的顶点 + 边严格内部交点，按逆时针串成。
package un

import (
	"errors"
	"math"
	"sort"
	"sync/atomic"

	"ontology/poly"
)

// ErrEmpty 表示并集退化（面积为零），按空处理。
var ErrEmpty = errors.New("un: degenerate empty union")

// probes 记录最近一次 Union 实际执行的边-边求交判定次数；非导出，不进公开接口。
var probes atomic.Int64

type edge struct {
	a, b                   poly.Point
	minx, miny, maxx, maxy float64
}
type seg struct{ a, b poly.Point }

func mkEdge(a, b poly.Point) edge {
	return edge{a, b, min(a.X, b.X), min(a.Y, b.Y), max(a.X, b.X), max(a.Y, b.Y)}
}

func (e edge) overlaps(o edge) bool {
	return e.minx <= o.maxx && o.minx <= e.maxx && e.miny <= o.maxy && o.miny <= e.maxy
}

func bboxOf(p []poly.Point) edge {
	bb := mkEdge(p[0], p[0])
	for _, v := range p[1:] {
		bb.minx, bb.miny, bb.maxx, bb.maxy = min(bb.minx, v.X), min(bb.miny, v.Y), max(bb.maxx, v.X), max(bb.maxy, v.Y)
	}
	return bb
}

// grid 用均匀网格装 B 的边，剪枝边-边求交，避免两两全配对。
type grid struct {
	inv  float64
	cell map[[2]int][]int
}

func newGrid(es []edge, bb edge) *grid {
	size := max(max(bb.maxx-bb.minx, bb.maxy-bb.miny)/(math.Sqrt(float64(len(es)))+1), 1)
	g := &grid{inv: 1 / size, cell: map[[2]int][]int{}}
	for i, e := range es {
		for x := int(e.minx * g.inv); x <= int(e.maxx*g.inv); x++ {
			for y := int(e.miny * g.inv); y <= int(e.maxy*g.inv); y++ {
				c := [2]int{x, y}
				g.cell[c] = append(g.cell[c], i)
			}
		}
	}
	return g
}

func param(a, b, p poly.Point) float64 {
	if dx := b.X - a.X; math.Abs(dx) >= math.Abs(b.Y-a.Y) {
		return (p.X - a.X) / dx
	}
	return (p.Y - a.Y) / (b.Y - a.Y)
}

// Union 返回 A∪B 的外边界（逆时针简单多边形）。假定 A、B 均为合法简单多边形。
func Union(A, B []poly.Point) ([]poly.Point, error) {
	probes.Store(0)
	ea, eb := edgesOf(A), edgesOf(B)
	g := newGrid(eb, bboxOf(B))
	hitsA, hitsB := map[int][]poly.Point{}, map[int][]poly.Point{}
	stamp, gen := make([]int, len(eb)), 0
	for i, e := range ea { // 网格剪枝 + 包围盒过滤后才真正求交
		gen++
		for x := int(e.minx * g.inv); x <= int(e.maxx*g.inv); x++ {
			for y := int(e.miny * g.inv); y <= int(e.maxy*g.inv); y++ {
				for _, j := range g.cell[[2]int{x, y}] {
					if stamp[j] != gen && e.overlaps(eb[j]) {
						stamp[j] = gen
						probes.Add(1)
						if p, ok := poly.SegIntersect(e.a, e.b, eb[j].a, eb[j].b); ok {
							hitsA[i] = append(hitsA[i], p)
							hitsB[j] = append(hitsB[j], p)
						}
					}
				}
			}
		}
	}
	return assemble(append(keptSegs(A, B, hitsA), keptSegs(B, A, hitsB)...))
}

func edgesOf(p []poly.Point) []edge {
	es := make([]edge, len(p))
	for i := range p {
		es[i] = mkEdge(p[i], p[(i+1)%len(p)])
	}
	return es
}

// keptSegs 返回 P 的边界中不在 Q 严格内部的有向子边（按交点切分后判定中点）。
func keptSegs(P, Q []poly.Point, hits map[int][]poly.Point) []seg {
	bb := bboxOf(Q)
	var out []seg
	for i := range P {
		a, b := P[i], P[(i+1)%len(P)]
		hs := hits[i]
		sort.Slice(hs, func(x, y int) bool { return param(a, b, hs[x]) < param(a, b, hs[y]) })
		prev := a
		for _, p := range append(hs, b) {
			mid := poly.Point{X: (prev.X + p.X) / 2, Y: (prev.Y + p.Y) / 2}
			in := mid.X >= bb.minx && mid.X <= bb.maxx && mid.Y >= bb.miny && mid.Y <= bb.maxy && poly.PointInPoly(mid, Q)
			if !in {
				out = append(out, seg{prev, p})
			}
			prev = p
		}
	}
	return out
}

func assemble(segs []seg) ([]poly.Point, error) {
	next, used := map[poly.Point]poly.Point{}, map[poly.Point]bool{}
	for _, s := range segs {
		next[s.a] = s.b
	}
	var best []poly.Point
	bestArea := 0.0
	for _, s := range segs {
		var loop []poly.Point
		for cur := s.a; !used[cur]; {
			used[cur] = true
			loop = append(loop, cur)
			nxt, ok := next[cur]
			if !ok || nxt == s.a {
				break
			}
			cur = nxt
		}
		if a2 := poly.Area2(loop); a2 > bestArea {
			bestArea, best = a2, loop
		}
	}
	if bestArea <= 0 {
		return nil, ErrEmpty
	}
	return best, nil
}
