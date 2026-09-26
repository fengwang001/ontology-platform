// Package api 对外提供最小包围圆服务，依赖 mec。
package api

import (
	"errors"
	"math/big"
	"sync"

	"ontology/circ"
	"ontology/mec"
)

// 可判定的哨兵错误，三者互不相同。
var (
	ErrDuplicate  = errors.New("api: 重复坐标点")
	ErrOutOfRange = errors.New("api: 坐标越界 |X|或|Y|>10^4")
	ErrEmpty      = errors.New("api: 空集求圆")
)

const limit = 10000

// Point 是整数坐标二维点。
type Point = circ.Point

// Circle 是最小包围圆的精确表示：有理数圆心与半径平方。
type Circle struct {
	Cx, Cy *big.Rat
	R2     *big.Rat
}

// API 并发安全地维护点集的最小包围圆。
type API struct {
	mu   sync.RWMutex
	m    *mec.MEC
	seen map[circ.Point]struct{}
}

// New 返回空实例。
func New() (*API, error) {
	return &API{m: mec.New(), seen: make(map[circ.Point]struct{})}, nil
}

// Insert 插入一个点；越界或重复时整体失败、状态不变。
func (a *API) Insert(x, y int) error {
	if x < -limit || x > limit || y < -limit || y > limit {
		return ErrOutOfRange
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	p := circ.Point{X: x, Y: y}
	if _, dup := a.seen[p]; dup {
		return ErrDuplicate
	}
	a.m.Insert(p)
	a.seen[p] = struct{}{}
	return nil
}

// MinCircle 返回当前最小包围圆；空集返回 ErrEmpty，状态不变。
func (a *API) MinCircle() (Circle, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	c, ok := a.m.Circle()
	if !ok {
		return Circle{}, ErrEmpty
	}
	return Circle{
		Cx: new(big.Rat).Set(c.Cx),
		Cy: new(big.Rat).Set(c.Cy),
		R2: new(big.Rat).Set(c.R2),
	}, nil
}

// Boundary 返回确定当前圆的 1/2/3 个边界点；空集返回 nil。
func (a *API) Boundary() []Point {
	a.mu.RLock()
	defer a.mu.RUnlock()
	c, ok := a.m.Circle()
	if !ok {
		return nil
	}
	return append([]Point(nil), c.B...)
}

// SelfCheck 对内置点集核验四条不变量：与暴力枚举一致、最小性、
// 覆盖正确、失败不留痕。只使用局部实例，可并发调用。
func (a *API) SelfCheck() error {
	sets := [][]circ.Point{
		{{X: 0, Y: 0}},
		{{X: 0, Y: 0}, {X: 6, Y: 0}},
		{{X: 0, Y: 0}, {X: 6, Y: 0}, {X: 0, Y: 8}},
		{{X: 0, Y: 0}, {X: 6, Y: 0}, {X: 1, Y: 1}},
		{{X: 0, Y: 0}, {X: 3, Y: 0}, {X: 6, Y: 0}},
		{{X: 3, Y: 1}, {X: -4, Y: 2}, {X: 5, Y: -3}, {X: 0, Y: 7}, {X: -2, Y: -6}},
	}
	for _, s := range sets {
		m := mec.New()
		for _, p := range s {
			m.Insert(p)
		}
		got, _ := m.Circle()
		if !got.Equal(mec.BruteForce(s)) { // 不变量 1、2
			return errors.New("api: 自检失败 与暴力枚举不一致")
		}
		for _, p := range s { // 不变量 3
			if !got.Contains(p) {
				return errors.New("api: 自检失败 点未被覆盖")
			}
		}
		for _, b := range got.B {
			if !got.OnBoundary(b) {
				return errors.New("api: 自检失败 边界点不在圆上")
			}
		}
	}
	fresh, _ := New() // 不变量 4：失败不留痕
	if _, err := fresh.MinCircle(); !errors.Is(err, ErrEmpty) {
		return errors.New("api: 自检失败 空集未报 ErrEmpty")
	}
	if err := fresh.Insert(1, 1); err != nil {
		return err
	}
	before, _ := fresh.MinCircle()
	if fresh.Insert(1, 1) != ErrDuplicate || fresh.Insert(10001, 0) != ErrOutOfRange {
		return errors.New("api: 自检失败 错误不可判定")
	}
	after, _ := fresh.MinCircle()
	if before.Cx.Cmp(after.Cx) != 0 || before.R2.Cmp(after.R2) != 0 || len(fresh.Boundary()) != 1 {
		return errors.New("api: 自检失败 被拒操作改变了状态")
	}
	return nil
}
