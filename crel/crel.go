// Package crel 维护多边形边集合（按空间网格分桶），
// 求圆心到边界的最短平方距离并做四态分类。依赖 cgeom。
package crel

import (
	"math"
	"sync/atomic"

	"ontology/cgeom"
)

// Relation 圆与多边形的四态关系。
type Relation int

const (
	Crossing  Relation = iota // D <  r² 相交
	Tangent                   // D == r² 相切
	Contained                 // D >  r² 且圆心在内
	Disjoint                  // D >  r² 且圆心在外
)

func (r Relation) String() string {
	return [...]string{"CROSSING", "TANGENT", "CONTAINED", "DISJOINT"}[r]
}

const cellSize = 512 // 网格单元边长；坐标 |v|≤1e4 时约 40×40 个单元

type cell struct{ x, y int }

// Engine 一个多边形的判定器；构造后只读，可并发查询。
type Engine struct {
	poly  []cgeom.Point
	edges [][2]cgeom.Point
	grid  map[cell][]int // 单元 -> 边下标（按边的包围盒分桶，超集）
	last  atomic.Int64   // 非导出计数器：最近一次 Relation 实际计算过距离的边条数
}

func fdiv(v int) int { // 向下取整除法
	if v >= 0 {
		return v / cellSize
	}
	return -((-v + cellSize - 1) / cellSize)
}

// New 建立边集合与网格分桶；假定多边形已合法（校验在 api 层）。
func New(poly []cgeom.Point) *Engine {
	e := &Engine{poly: poly, grid: map[cell][]int{}}
	for i := range poly {
		a, b := poly[i], poly[(i+1)%len(poly)]
		idx := len(e.edges)
		e.edges = append(e.edges, [2]cgeom.Point{a, b})
		for cx := fdiv(min(a.X, b.X)); cx <= fdiv(max(a.X, b.X)); cx++ {
			for cy := fdiv(min(a.Y, b.Y)); cy <= fdiv(max(a.Y, b.Y)); cy++ {
				c := cell{cx, cy}
				e.grid[c] = append(e.grid[c], idx)
			}
		}
	}
	return e
}

// minDist2 网格环形扩展求边界最短平方距离：环 k 之外任意点到 p 的
// 平方距离 > (k·cellSize)²，故 best ≤ 该下界即可停，绝不漏边。
func (e *Engine) minDist2(p cgeom.Point) (best cgeom.Rat, cnt int64) {
	best = cgeom.Rat{N: math.MaxInt64, D: 1}
	seen := make([]bool, len(e.edges))
	cx, cy := fdiv(p.X), fdiv(p.Y)
	for k := 0; ; k++ {
		for dx := -k; dx <= k; dx++ {
			for dy := -k; dy <= k; dy++ {
				if max(abs(dx), abs(dy)) != k {
					continue
				}
				for _, ei := range e.grid[cell{cx + dx, cy + dy}] {
					if seen[ei] {
						continue
					}
					seen[ei] = true
					cnt++
					if d := cgeom.PointSegDist2(p, e.edges[ei][0], e.edges[ei][1]); d.Cmp(best) < 0 {
						best = d
					}
				}
			}
		}
		lim := int64(k) * cellSize
		if best.Cmp(cgeom.Rat{N: lim * lim, D: 1}) <= 0 {
			return best, cnt
		}
	}
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// r2Of 半径平方；r 极大时饱和到 MaxInt64（仍大于任何可能的 d²，比较结果不变）。
func r2Of(r int) int64 {
	if r > 1<<31 {
		return math.MaxInt64
	}
	return int64(r) * int64(r)
}

// classify 四态分类：先判相等（相切），再判小于（相交），最后按内外分内含/相离。
func classify(d cgeom.Rat, r2 int64, inside bool) Relation {
	switch c := d.Cmp(cgeom.Rat{N: r2, D: 1}); {
	case c == 0:
		return Tangent
	case c < 0:
		return Crossing
	case inside:
		return Contained
	default:
		return Disjoint
	}
}

// Relation 判定圆 (cx,cy,r) 与多边形的关系；输入假定已校验。
func (e *Engine) Relation(cx, cy, r int) Relation {
	p := cgeom.Point{X: cx, Y: cy}
	best, cnt := e.minDist2(p)
	e.last.Store(cnt)
	return classify(best, r2Of(r), cgeom.PointInPoly(p, e.poly))
}

// NaiveRelation O(n) 朴素参照：逐边算截断平方距离取最小，再射线法判内外。
func NaiveRelation(poly []cgeom.Point, cx, cy, r int) Relation {
	p := cgeom.Point{X: cx, Y: cy}
	best := cgeom.Rat{N: math.MaxInt64, D: 1}
	for i := range poly {
		if d := cgeom.PointSegDist2(p, poly[i], poly[(i+1)%len(poly)]); d.Cmp(best) < 0 {
			best = d
		}
	}
	return classify(best, r2Of(r), cgeom.PointInPoly(p, poly))
}

// CheckLocality 自检：多档规模 m 的边均匀铺满大空间，小圆在角落查询，
// 实际计算距离的边条数须 ≤ 与 m 无关的小常数。只返回通过与否，不暴露计数。
func CheckLocality() bool {
	for _, side := range []int{10, 32, 100} { // m = side² = 100 / 1024 / 10000
		step := 20000 / (side - 1)
		var poly []cgeom.Point
		for i := 0; i < side; i++ {
			for j := 0; j < side; j++ {
				jj := j
				if i%2 == 1 { // 蛇形排列，保证相邻顶点相近
					jj = side - 1 - j
				}
				poly = append(poly, cgeom.Point{X: -10000 + jj*step, Y: -10000 + i*step})
			}
		}
		e := New(poly)
		e.Relation(-10000, -10000, 1)
		if e.last.Load() > 64 {
			return false
		}
	}
	return true
}
