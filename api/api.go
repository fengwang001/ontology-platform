// Package api 对外提供最小包围圆服务：插入点、读圆、读边界、自检。
package api

import (
	"errors"
	"fmt"
	"sync"

	"ontology/circ"
	"ontology/mec"
)

// 三类可判定且互不相同的哨兵错误。
var (
	ErrOutOfRange = errors.New("api: coordinate out of range")
	ErrDuplicate  = errors.New("api: duplicate point")
	ErrEmpty      = errors.New("api: empty point set")
)

const maxCoord = 10000

type (
	Point  = circ.Point  // Point 对外即 circ 的整数点。
	Circle = circ.Circle // Circle 对外即 circ 的精确圆。
)

// Service 是并发安全的最小包围圆服务。
type Service struct {
	mu   sync.RWMutex
	m    *mec.MEC
	seen map[Point]struct{}
}

// New 返回空服务；error 恒为 nil，保留以便将来引入构造期校验。
func New() (*Service, error) {
	return &Service{m: mec.New(), seen: make(map[Point]struct{})}, nil
}

// Insert 插入一个点；越界或重复时整体失败、状态不变（先校验、后改状态）。
func (s *Service) Insert(x, y int) error {
	if x < -maxCoord || x > maxCoord || y < -maxCoord || y > maxCoord {
		return ErrOutOfRange
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p := Point{X: x, Y: y}
	if _, ok := s.seen[p]; ok {
		return ErrDuplicate
	}
	s.seen[p] = struct{}{}
	s.m.Insert(p)
	return nil
}

// MinCircle 返回当前最小包围圆（圆心有理数、半径平方）；空集返回 ErrEmpty。
func (s *Service) MinCircle() (Circle, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c, ok := s.m.Circle()
	if !ok {
		return Circle{}, ErrEmpty
	}
	c.B = append([]Point(nil), c.B...)
	return c, nil
}

// Boundary 返回当前圆边界上的 1/2/3 个点；空集返回 nil。
func (s *Service) Boundary() []Point {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c, ok := s.m.Circle()
	if !ok {
		return nil
	}
	return append([]Point(nil), c.B...)
}

// bruteForce 枚举单点、点对直径、三点圆（含回退），返回覆盖全部点的最小者。
func bruteForce(pts []Point) Circle {
	var best Circle
	have := false
	try := func(c Circle) {
		for _, p := range pts {
			if !c.Contains(p) {
				return
			}
		}
		if !have || c.R2.Cmp(best.R2) < 0 {
			best, have = c, true
		}
	}
	for i, a := range pts {
		try(circ.FromPoint(a))
		for j := i + 1; j < len(pts); j++ {
			try(circ.FromDiameter(a, pts[j]))
			for k := j + 1; k < len(pts); k++ {
				try(circ.FromThree(a, pts[j], pts[k]))
			}
		}
	}
	return best
}

// checkSet 核验一组点的四条不变量：与暴力一致（含最小性）、覆盖、边界在圆上。
func checkSet(pts []Point) error {
	svc, _ := New()
	for _, p := range pts {
		if err := svc.Insert(p.X, p.Y); err != nil {
			return err
		}
	}
	got, err := svc.MinCircle()
	if err != nil {
		return err
	}
	want := bruteForce(pts)
	if got.Cx.Cmp(want.Cx) != 0 || got.Cy.Cmp(want.Cy) != 0 || got.R2.Cmp(want.R2) != 0 {
		return fmt.Errorf("api: MEC %v,%v,%v != brute %v,%v,%v",
			got.Cx, got.Cy, got.R2, want.Cx, want.Cy, want.R2)
	}
	for _, p := range pts {
		if !got.Contains(p) {
			return fmt.Errorf("api: point %v not covered", p)
		}
	}
	for _, p := range svc.Boundary() {
		if !got.OnBoundary(p) {
			return fmt.Errorf("api: boundary point %v not on circle", p)
		}
	}
	return nil
}

// SelfCheck 对内置点集核验四条不变量，全部通过返回 nil。
// 只使用本地新建的实例，不触碰调用者状态，可并发调用。
func (s *Service) SelfCheck() error {
	sets := [][]Point{
		{{X: 0, Y: 0}}, {{X: 0, Y: 0}, {X: 6, Y: 0}},
		{{X: 0, Y: 0}, {X: 6, Y: 0}, {X: 0, Y: 8}}, {{X: 0, Y: 0}, {X: 6, Y: 0}, {X: 1, Y: 1}},
		{{X: 0, Y: 0}, {X: 3, Y: 0}, {X: 6, Y: 0}},
		{{X: -10000, Y: -10000}, {X: 10000, Y: 10000}, {X: -10000, Y: 10000}, {X: 10000, Y: -10000}},
		{{X: 3, Y: 1}, {X: -7, Y: 4}, {X: 5, Y: -9}, {X: 0, Y: 0}, {X: -2, Y: -6}, {X: 8, Y: 8}, {X: -4, Y: 10}, {X: 6, Y: -3}},
	}
	for i, set := range sets {
		if err := checkSet(set); err != nil {
			return fmt.Errorf("api: self-check set %d: %w", i, err)
		}
	}
	return nil
}
