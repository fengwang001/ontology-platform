// Package hpi 维护半平面交区域：从包围正方形出发逐个半平面裁剪。
package hpi

import (
	"errors"
	"math/big"
	"sync"

	"ontology/hp"
)

// Bound 是隐式包围正方形 [-Bound,Bound]^2 的半边长，大到不会截掉结果顶点。
const Bound int64 = 1_000_000

const maxCoef = 10000

// 两类可判定哨兵错误，互不相同；任何拒绝都整体失败、不改变状态。
var (
	ErrDegenerate = errors.New("hpi: degenerate half-plane (a,b)=(0,0)")
	ErrOutOfRange = errors.New("hpi: |a|,|b|,|c| must be <= 1e4")
)

// Region 是半平面交区域，并发安全。零值不可用，请用 New。
type Region struct {
	mu                     sync.RWMutex
	verts                  []hp.Point
	hps                    []hp.HalfPlane
	minX, maxX, minY, maxY *big.Rat
	edgesChecked           int // 最近一次 Add 实际行走的边数；O(1) 预判命中为 0。非导出
}

// New 返回包围正方形区域（逆时针、首尾不重复）。
func New() *Region {
	m, nm := big.NewRat(Bound, 1), big.NewRat(-Bound, 1)
	return &Region{
		verts: []hp.Point{{X: nm, Y: nm}, {X: m, Y: nm}, {X: m, Y: m}, {X: nm, Y: m}},
		minX:  new(big.Rat).Set(nm), maxX: new(big.Rat).Set(m), minY: new(big.Rat).Set(nm), maxY: new(big.Rat).Set(m),
	}
}

// Add 校验并用 h 裁剪当前区域（边界 Side==0 算在内）；拒绝则整体失败不留痕。
func (r *Region) Add(h hp.HalfPlane) error {
	if h.A == 0 && h.B == 0 {
		return ErrDegenerate
	}
	if h.A < -maxCoef || h.A > maxCoef || h.B < -maxCoef || h.B > maxCoef || h.C < -maxCoef || h.C > maxCoef {
		return ErrOutOfRange
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.hps = append(r.hps, h)
	r.edgesChecked = 0
	if len(r.verts) == 0 || r.redundant(h) {
		return nil
	}
	r.verts = clip(r.verts, h, &r.edgesChecked)
	if len(r.verts) >= 3 {
		r.recomputeBox()
	} else {
		r.verts = nil // 退化为点/线段：交集无面积，按空处理
	}
	return nil
}

// redundant 用包围盒做 O(1) 预判：A·x+B·y 在矩形上的最大值点取各系数符号端，最大值<=C 即整块在内。
func (r *Region) redundant(h hp.HalfPlane) bool {
	x, y := r.minX, r.minY
	if h.A > 0 {
		x = r.maxX
	}
	if h.B > 0 {
		y = r.maxY
	}
	return hp.Side(h, hp.Point{X: x, Y: y}).Sign() <= 0
}

// clip 是 Sutherland–Hodgman 多边形裁剪，全程精确有理运算并做相邻去重。
func clip(poly []hp.Point, h hp.HalfPlane, checked *int) []hp.Point {
	out := make([]hp.Point, 0, len(poly)+2)
	add := func(p hp.Point) {
		if len(out) == 0 || !hp.Eq(out[len(out)-1], p) {
			out = append(out, p)
		}
	}
	for i, p := range poly {
		q := poly[(i+1)%len(poly)]
		*checked++
		inP, inQ := hp.Side(h, p).Sign() <= 0, hp.Side(h, q).Sign() <= 0
		if inP != inQ {
			if z, ok := hp.Intersect(h, p, q); ok {
				add(z)
			}
		}
		if inQ {
			add(q)
		}
	}
	if n := len(out); n >= 2 && hp.Eq(out[0], out[n-1]) {
		out = out[:n-1]
	}
	if len(out) < 3 {
		return nil
	}
	return canonical(out)
}

// canonical 旋转顶点序列使字典序最小顶点在最前，输出确定且仍为 CCW。
func canonical(poly []hp.Point) []hp.Point {
	best := 0
	for i := 1; i < len(poly); i++ {
		if c := poly[i].X.Cmp(poly[best].X); c < 0 || (c == 0 && poly[i].Y.Cmp(poly[best].Y) < 0) {
			best = i
		}
	}
	return append(append([]hp.Point{}, poly[best:]...), poly[:best]...)
}

// recomputeBox 重算轴对齐包围盒（持锁调用）。
func (r *Region) recomputeBox() {
	r.minX.Set(r.verts[0].X)
	r.maxX.Set(r.verts[0].X)
	r.minY.Set(r.verts[0].Y)
	r.maxY.Set(r.verts[0].Y)
	for _, p := range r.verts[1:] {
		if p.X.Cmp(r.minX) < 0 {
			r.minX.Set(p.X)
		} else if p.X.Cmp(r.maxX) > 0 {
			r.maxX.Set(p.X)
		}
		if p.Y.Cmp(r.minY) < 0 {
			r.minY.Set(p.Y)
		} else if p.Y.Cmp(r.maxY) > 0 {
			r.maxY.Set(p.Y)
		}
	}
}

// Verts 返回区域顶点（逆时针）的深拷贝；空区域返回空切片。
func (r *Region) Verts() []hp.Point {
	r.mu.RLock()
	defer r.mu.RUnlock()
	cp := make([]hp.Point, 0, len(r.verts))
	for _, p := range r.verts {
		cp = append(cp, hp.Point{X: new(big.Rat).Set(p.X), Y: new(big.Rat).Set(p.Y)})
	}
	return cp
}
