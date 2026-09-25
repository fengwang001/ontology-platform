// Package api 对外接口：New / Feed / View / Last / SelfCheck。依赖 fill。
package api

import (
	"errors"
	"fmt"
	"sync"

	"ontology/fill"
	"ontology/grid"
)

// ErrBadStep 步长非法（step <= 0）。
var ErrBadStep = errors.New("api: step must be positive")

// Point 对外暴露的点类型。
type Point = grid.Point

// API 是并发安全的填充序列服务。
type API struct {
	mu sync.RWMutex
	f  *fill.Filler
}

// New 创建实例；step <= 0 返回 ErrBadStep。
func New(step int64) (*API, error) {
	if step <= 0 {
		return nil, ErrBadStep
	}
	return &API{f: fill.NewFiller(step)}, nil
}

// Feed 喂一批点；任一点非法则整批拒绝、不留痕。
func (a *API) Feed(pts []Point) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.f.Feed(pts)
}

// View 返回该键填充后的完整序列（TS 升序）。
func (a *API) View(key string) []Point {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.f.View(key)
}

// Last 返回该键最后一个真实点。
func (a *API) Last(key string) (Point, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.f.Last(key)
}

// SelfCheck 对内置点序列核验四条不变量，全部通过返回 nil。
func (a *API) SelfCheck() error {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return selfCheck(a.f)
}

func selfCheck(f *fill.Filler) error {
	f2 := fill.NewFiller(10)
	pts := []grid.Point{{Key: "k", TS: 0, Val: 5}, {Key: "k", TS: 30, Val: 8},
		{Key: "k", TS: 40, Val: 8}, {Key: "k", TS: 70, Val: 12}, {Key: "k", TS: 100, Val: 12}}
	if err := f2.Feed(pts); err != nil {
		return fmt.Errorf("selfcheck feed: %w", err)
	}
	v := f2.View("k")
	// 不变量 1+3：与逐网格重放 LOCF 的朴素参照逐点一致，且网格时刻无遗漏无多余
	var cur int64
	pi := 0
	naive := map[int64]int64{}
	for g := pts[0].TS; g <= pts[len(pts)-1].TS; g += 10 {
		for pi < len(pts) && pts[pi].TS <= g {
			cur, pi = pts[pi].Val, pi+1
		}
		naive[g] = cur
	}
	if len(v) != len(naive) {
		return errors.New("selfcheck: view length != naive")
	}
	for i, p := range v {
		if want := pts[0].TS + int64(i)*10; p.TS != want || naive[p.TS] != p.Val {
			return fmt.Errorf("selfcheck: mismatch at %d", p.TS)
		}
		if i > 0 && p.Val < v[i-1].Val { // 不变量 2：单调不减
			return errors.New("selfcheck: not non-decreasing")
		}
	}
	// 不变量 4：失败不留痕
	before := len(f2.View("k"))
	if err := f2.Feed([]grid.Point{{Key: "k", TS: 105, Val: 1}}); !errors.Is(err, grid.ErrOffGrid) {
		return errors.New("selfcheck: off-grid not rejected")
	}
	if len(f2.View("k")) != before {
		return errors.New("selfcheck: rejected feed left trace")
	}
	return nil
}
