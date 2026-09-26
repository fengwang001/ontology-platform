// Package api 对外提供凸多边形直径接口：校验、求直径、自检。依赖 rc。
package api

import (
	"errors"
	"fmt"

	"ontology/cal"
	"ontology/rc"
)

// Point 是顶点坐标类型，|X|,|Y| <= 10^4。
type Point = cal.Point

var (
	ErrTooFewVertices = errors.New("ontology: fewer than 3 vertices")
	ErrOutOfRange     = errors.New("ontology: coordinate |X| or |Y| exceeds 10^4")
	ErrNotConvex      = errors.New("ontology: polygon not strictly convex CCW, or duplicate vertex, or self-intersecting")
	ErrNotInitialized = errors.New("ontology: polygon not initialized")
	errInvariant      = errors.New("ontology: self-check invariant violated")
)

const maxCoord = 10000

type Polygon struct{ poly []cal.Point }

// New 校验 poly（凸、严格逆时针、无重复、顶点数>=3、坐标不越界），全部通过
// 才整体替换接收者状态；任何一步失败都不留部分结果。
func (p *Polygon) New(poly []Point) error {
	if err := validate(poly); err != nil {
		return err
	}
	p.poly = append([]cal.Point(nil), poly...)
	return nil
}

func validate(poly []cal.Point) error {
	n := len(poly)
	if n < 3 {
		return ErrTooFewVertices
	}
	seen := map[cal.Point]bool{}
	for _, v := range poly {
		if v.X < -maxCoord || v.X > maxCoord || v.Y < -maxCoord || v.Y > maxCoord {
			return ErrOutOfRange
		}
		if seen[v] {
			return ErrNotConvex
		}
		seen[v] = true
	}
	for i := 0; i < n; i++ { // 严格凸且逆时针：所有连续转向一律左转
		if cal.Orient(poly[i], poly[(i+1)%n], poly[(i+2)%n]) <= 0 {
			return ErrNotConvex
		}
	}
	for i := 0; i < n; i++ { // 全左转仍可能自交（星形），补 O(n²) 相交检查
		for j := i + 2; j < n && !(i == 0 && j == n-1); j++ {
			a, b, c, d := poly[i], poly[(i+1)%n], poly[j], poly[(j+1)%n]
			if cal.Orient(a, b, c)*cal.Orient(a, b, d) < 0 && cal.Orient(c, d, a)*cal.Orient(c, d, b) < 0 {
				return ErrNotConvex
			}
		}
	}
	return nil
}

// Diameter 返回最大平方距离与全部并列的顶点索引对。可并发调用。
func (p *Polygon) Diameter() (int64, [][2]int, error) {
	if p.poly == nil {
		return 0, nil, ErrNotInitialized
	}
	d2, pairs := rc.Diameter(p.poly)
	return d2, pairs, nil
}

// SelfCheck 对内置凸多边形核验四条不变量（暴力一致/对跖对/并列完整/计数线性）。
func (p *Polygon) SelfCheck() error {
	builtins := [][]cal.Point{
		{{X: 0, Y: 0}, {X: 5, Y: 1}, {X: 6, Y: 4}, {X: 3, Y: 6}, {X: 1, Y: 5}},                 // 第三节五边形
		{{X: 0, Y: 0}, {X: 6, Y: 0}, {X: 6, Y: 4}, {X: 0, Y: 4}},                               // 矩形，两对并列
		{{X: 0, Y: 0}, {X: 4, Y: 0}, {X: 4, Y: 4}, {X: 0, Y: 4}},                               // 正方形，两对并列
		{{X: 0, Y: 0}, {X: 7, Y: 2}, {X: 5, Y: 9}},                                             // 三角形
		{{X: 0, Y: 0}, {X: 4, Y: 1}, {X: 6, Y: 5}, {X: 3, Y: 8}, {X: -1, Y: 6}, {X: -2, Y: 3}}, // 六边形
	}
	for _, poly := range builtins {
		var g Polygon
		if err := g.New(poly); err != nil {
			return fmt.Errorf("%w: builtin rejected: %v", errInvariant, err)
		}
		d2, pairs, _ := g.Diameter() // New 已成功，Diameter 不会再失败
		best, maxPairs := brute(poly)
		if d2 != best { // 不变量 1
			return fmt.Errorf("%w: d2 %d != brute %d", errInvariant, d2, best)
		}
		for _, pr := range pairs { // 不变量 2、3
			if !antipodal(poly, pr[0], pr[1]) {
				return fmt.Errorf("%w: pair %v not antipodal", errInvariant, pr)
			}
			delete(maxPairs, pr)
		}
		if len(maxPairs) > 0 {
			return fmt.Errorf("%w: tied pairs missing: %v", errInvariant, maxPairs)
		}
	}
	return rc.SelfCheck() // 不变量：计数线性
}

func brute(poly []cal.Point) (int64, map[[2]int]bool) {
	best, out := int64(-1), map[[2]int]bool{}
	for i := range poly {
		for j := i + 1; j < len(poly); j++ {
			if d := cal.Dist2(poly[i], poly[j]); d >= best {
				if d > best {
					best, out = d, map[[2]int]bool{}
				}
				out[[2]int{i, j}] = true
			}
		}
	}
	return best, out
}

// antipodal 判定无序对 {a,b} 是否为对跖点对：一端是另一端某条关联边
// （(v→v+1) 或 (v-1→v)）的对跖顶点（到边所在直线距离并列最大）。
func antipodal(poly []cal.Point, a, b int) bool {
	n := len(poly)
	for _, t := range [4][2]int{{a, b}, {(a - 1 + n) % n, b}, {b, a}, {(b - 1 + n) % n, a}} {
		x, y := poly[t[0]], poly[(t[0]+1)%n]
		m, ok := abs(cal.Area2(x, y, poly[t[1]])), true
		for k := 0; k < n && ok; k++ {
			ok = abs(cal.Area2(x, y, poly[k])) <= m
		}
		if ok {
			return true
		}
	}
	return false
}

func abs(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}
