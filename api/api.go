// Package api 是对外门面：并发安全地组合 view 与 dag，错误一律为可判定哨兵错误。
package api

import (
	"errors"
	"fmt"
	"sync"

	"ontology/view"
)

var (
	ErrEmptyName     = errors.New("api: empty view name")
	ErrDuplicateName = errors.New("api: view name already exists")
	ErrUnknownView   = errors.New("api: unknown view")
	ErrNotBase       = errors.New("api: Set only applies to base views")
)

// Engine 是物化视图引擎，全部方法可并发调用。
type Engine struct {
	mu sync.RWMutex
	st *view.Store
}

func New() *Engine { return &Engine{st: view.New()} }

// AddView 注册视图，允许前向引用。空名、重名、成环都会被拒且状态不变。
func (e *Engine) AddView(name string, deps []string, fn func(...int64) int64) error {
	if name == "" {
		return ErrEmptyName
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.st.Has(name) {
		return ErrDuplicateName
	}
	return e.st.Add(name, deps, fn)
}

// Set 给基视图置值并级联标脏；未注册名或非基视图被拒且状态不变。
func (e *Engine) Set(name string, val int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.st.Has(name) {
		return ErrUnknownView
	}
	if e.st.IsBase(name) {
		e.st.Set(name, val)
		return nil
	}
	return ErrNotBase
}

// Recompute 按拓扑序重算全部脏视图；未解析依赖或成环则整体失败。
func (e *Engine) Recompute() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.st.Recompute()
}

// Get 读取视图当前值；ok 报告是否已有值。未注册名返回 ErrUnknownView。
func (e *Engine) Get(name string) (int64, bool, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if !e.st.Has(name) {
		return 0, false, ErrUnknownView
	}
	v, ok := e.st.Get(name)
	return v, ok, nil
}

// SelfCheck 在独立实例上核验四条不变量，可并发调用，不影响本实例。
func (e *Engine) SelfCheck() error {
	eng := New() // 不变量 1+2：第三节场景对拍手推最终值
	reg := []struct {
		n string
		d []string
		f func(...int64) int64
	}{
		{"A", nil, nil}, {"B", nil, nil},
		{"E", []string{"C", "D"}, func(a ...int64) int64 { return a[0] + a[1] }},
		{"F", []string{"D"}, func(a ...int64) int64 { return a[0] - 1 }},
		{"C", []string{"A", "B"}, func(a ...int64) int64 { return a[0] + a[1] }},
		{"D", []string{"A"}, func(a ...int64) int64 { return a[0] * 2 }},
	}
	for _, r := range reg {
		if err := eng.AddView(r.n, r.d, r.f); err != nil {
			return err
		}
	}
	for _, op := range []func() error{
		func() error { return eng.Set("A", 1) }, func() error { return eng.Set("B", 2) },
		eng.Recompute, func() error { return eng.Set("B", 20) }, eng.Recompute,
		func() error { return eng.Set("A", 10) }, eng.Recompute,
	} {
		if err := op(); err != nil {
			return err
		}
	}
	for n, want := range map[string]int64{"A": 10, "B": 20, "C": 30, "D": 20, "E": 50, "F": 19} {
		if got, _, _ := eng.Get(n); got != want {
			return fmt.Errorf("selfcheck: %s=%d want %d", n, got, want)
		}
	}
	// 不变量 3：一轮内多次失效只求值一次（fn 副作用计数）
	evals := 0
	eng2 := New()
	_ = eng2.AddView("X", nil, nil)
	_ = eng2.AddView("Y", []string{"X"}, func(a ...int64) int64 { evals++; return a[0] })
	_ = eng2.Set("X", 1)
	_ = eng2.Set("X", 2)
	if err := eng2.Recompute(); err != nil || evals != 1 {
		return fmt.Errorf("selfcheck: dedup evals=%d err=%v", evals, err)
	}
	// 不变量 4：被拒操作不改变状态，引擎仍可正常使用
	before, _, _ := eng.Get("E")
	for _, err := range []error{
		eng.AddView("", nil, nil), eng.AddView("A", nil, nil),
		eng.Set("nobody", 1), eng.Set("C", 1),
	} {
		if err == nil {
			return errors.New("selfcheck: rejected op returned nil error")
		}
	}
	if after, _, _ := eng.Get("E"); after != before {
		return errors.New("selfcheck: rejected op changed state")
	}
	return eng.Recompute()
}
