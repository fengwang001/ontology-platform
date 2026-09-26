// Package api 对外提供凸多边形直径查询：校验、Diameter、SelfCheck。
package api

import (
	"errors"
	"fmt"
	"sync"

	"ontology/cal"
	"ontology/rc"
)

// Point 是 cal.Point 的别名，对外只暴露此类型。
type Point = cal.Point

var (
	ErrTooFewVertices = errors.New("api: 顶点数不足（少于 3 个）")
	ErrOutOfRange     = errors.New("api: 坐标越界（|X|>1e4 或 |Y|>1e4）")
	ErrNotConvex      = errors.New("api: 多边形非凸/自交/顶点重复/非逆时针")
	ErrNotInitialized = errors.New("api: 尚未 New 成功")
)

const maxCoord = 10000

var mu sync.RWMutex
var cur *rc.Caliper

func New(poly []Point) error {
	p, err := validate(poly)
	if err != nil {
		return err
	}
	mu.Lock()
	cur = rc.New(p)
	mu.Unlock()
	return nil
}

func Diameter() (int64, [][2]int, error) {
	mu.RLock()
	defer mu.RUnlock()
	if cur == nil {
		return 0, nil, ErrNotInitialized
	}
	d2, pairs := cur.Diameter()
	return d2, pairs, nil
}

func validate(poly []Point) ([]cal.Point, error) {
	n := len(poly)
	if n < 3 {
		return nil, ErrTooFewVertices
	}
	seen := make(map[cal.Point]bool, n)
	var twiceArea int64
	for i, p := range poly {
		if p.X > maxCoord || p.X < -maxCoord || p.Y > maxCoord || p.Y < -maxCoord {
			return nil, ErrOutOfRange
		}
		if seen[p] || cal.Orient(p, poly[(i+1)%n], poly[(i+2)%n]) <= 0 {
			return nil, ErrNotConvex // 顶点重复/非凸/自交/共线
		}
		seen[p] = true
		q := poly[(i+1)%n]
		twiceArea += p.X*q.Y - q.X*p.Y
	}
	if twiceArea <= 0 { // 非逆时针（含退化）
		return nil, ErrNotConvex
	}
	return poly, nil // rc.New 内部会复制
}

func SelfCheck() error {
	polys := [][]Point{{{X: 0, Y: 0}, {X: 5, Y: 1}, {X: 6, Y: 4}, {X: 3, Y: 6}, {X: 1, Y: 5}}, {{X: 0, Y: 0}, {X: 6, Y: 0}, {X: 6, Y: 4}, {X: 0, Y: 4}}, {{X: 0, Y: 0}, {X: 4, Y: 0}, {X: 2, Y: 3}}}
	for i, poly := range polys {
		if err := checkInvariants(poly); err != nil {
			return fmt.Errorf("自检多边形 %d: %w", i, err)
		}
	}
	bad := [][]Point{{{X: 0, Y: 0}, {X: 1, Y: 1}}, {{X: 0, Y: 0}, {X: 20000, Y: 0}, {X: 0, Y: 5}}, {{X: 0, Y: 0}, {X: 4, Y: 0}, {X: 4, Y: 4}, {X: 2, Y: 2}}, {{X: 0, Y: 0}, {X: 4, Y: 4}, {X: 0, Y: 4}, {X: 4, Y: 0}}}
	for _, poly := range bad { // 顶点不足/越界/凹/自交，必须整体拒绝
		if _, err := validate(poly); err == nil {
			return fmt.Errorf("非法输入未被拒绝: %v", poly)
		}
	}
	return nil
}

func checkInvariants(poly []Point) error {
	d2, pairs := rc.New(poly).Diameter()
	if bd2 := bruteMaxD2(poly); d2 != bd2 {
		return fmt.Errorf("不变量1: 卡壳 d²=%d ≠ 暴力 %d", d2, bd2)
	}
	ap := allAntipodalPairs(poly)
	got := make(map[[2]int]bool, len(pairs))
	for _, pr := range pairs {
		if d, ok := ap[pr]; !ok || d != d2 {
			return fmt.Errorf("不变量2: 点对 %v 非对跖点对或 d²≠最大", pr)
		}
		got[pr] = true
	}
	for k, d := range ap {
		if d == d2 && !got[k] {
			return fmt.Errorf("不变量3: 漏报并列对跖点对 %v", k)
		}
	}
	return nil
}

func bruteMaxD2(poly []Point) int64 {
	best := int64(-1)
	for i := range poly {
		for j := i + 1; j < len(poly); j++ {
			best = max(best, cal.Dist2(poly[i], poly[j]))
		}
	}
	return best
}

func allAntipodalPairs(poly []Point) map[[2]int]int64 {
	n := len(poly)
	out := make(map[[2]int]int64)
	for i := 0; i < n; i++ {
		a, b := poly[i], poly[(i+1)%n]
		best := int64(-1)
		for j := 0; j < n; j++ {
			best = max(best, absArea2(a, b, poly[j]))
		}
		for j := 0; j < n; j++ {
			if absArea2(a, b, poly[j]) != best {
				continue
			}
			for _, k := range [2]int{i, (i + 1) % n} {
				if k != j {
					key := [2]int{min(k, j), max(k, j)}
					out[key] = cal.Dist2(poly[key[0]], poly[key[1]])
				}
			}
		}
	}
	return out
}

func absArea2(a, b, c Point) int64 {
	ar := cal.Area2(a, b, c)
	if ar < 0 {
		return -ar
	}
	return ar
}
