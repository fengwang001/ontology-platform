// Package api 延迟物化引擎的对外接口。
package api

import (
	"errors"
	"fmt"

	"ontology/dep"
	"ontology/mat"
)

// Engine 对外引擎句柄。
type Engine struct{ e *mat.Engine }

// New 校验 spec 并构建引擎；含环或依赖未声明列时报错，不计算任何派生列。
func New(spec dep.Spec) (*Engine, error) {
	e, err := mat.NewEngine(spec)
	if err != nil {
		return nil, err
	}
	return &Engine{e}, nil
}

// Get 返回列当前值；未知列返回 mat.ErrUnknownColumn。
func (e *Engine) Get(name string) (int, error) { return e.e.Get(name) }

// Set 更新基列；未知列返回 mat.ErrUnknownColumn，派生列返回 mat.ErrSetDerived。
func (e *Engine) Set(name string, val int) error { return e.e.Set(name, val) }

// selfSpec 内置自检用列图：a=2,b=3,c=4；d=a+b, e=d*2, f=c+e。
func selfSpec() dep.Spec {
	return dep.Spec{
		Base: map[string]int{"a": 2, "b": 3, "c": 4},
		Derived: map[string]dep.Derived{
			"d": {Deps: []string{"a", "b"}, Fn: func(v []int) int { return v[0] + v[1] }},
			"e": {Deps: []string{"d"}, Fn: func(v []int) int { return v[0] * 2 }},
			"f": {Deps: []string{"c", "e"}, Fn: func(v []int) int { return v[0] + v[1] }},
		},
	}
}

// naive 朴素全量即时重算：不缓存，按依赖图从头算。
func naive(s dep.Spec, base map[string]int, name string) int {
	if v, ok := base[name]; ok {
		return v
	}
	d := s.Derived[name]
	args := make([]int, len(d.Deps))
	for i, dn := range d.Deps {
		args[i] = naive(s, base, dn)
	}
	return d.Fn(args)
}

// SelfCheck 对内置操作序列核验四条不变量，全部通过返回 nil。
func (e *Engine) SelfCheck() error {
	spec := selfSpec()
	eng, err := New(spec)
	if err != nil {
		return err
	}
	base := map[string]int{"a": 2, "b": 3, "c": 4}
	cols := []string{"a", "b", "c", "d", "e", "f"}
	allEq := func() error { // 不变量 1、2：任意交错后与朴素重算一致
		for _, c := range cols {
			got, err := eng.Get(c)
			if err != nil {
				return err
			}
			if want := naive(spec, base, c); got != want {
				return fmt.Errorf("selfcheck: %s=%d, naive=%d", c, got, want)
			}
		}
		return nil
	}
	seq := []struct {
		get bool
		col string
		v   int
	}{
		{true, "e", 0}, {true, "d", 0}, {true, "f", 0},
		{false, "a", 5}, {true, "d", 0}, {true, "f", 0}, {true, "e", 0},
		{false, "c", 10}, {true, "f", 0},
		{false, "b", 100}, {true, "d", 0}, // 不变量 3：失效后传递依赖取新值
	}
	for _, s := range seq {
		if s.get {
			if _, err := eng.Get(s.col); err != nil {
				return err
			}
		} else {
			if err := eng.Set(s.col, s.v); err != nil {
				return err
			}
			base[s.col] = s.v
		}
		if err := allEq(); err != nil {
			return err
		}
	}
	// 不变量 4：失败不留痕——四类拒绝后状态不变。
	before := map[string]int{}
	for _, c := range cols {
		before[c], _ = eng.Get(c)
	}
	if _, err := eng.Get("zzz"); !errors.Is(err, mat.ErrUnknownColumn) {
		return fmt.Errorf("selfcheck: get unknown: %v", err)
	}
	if err := eng.Set("zzz", 1); !errors.Is(err, mat.ErrUnknownColumn) {
		return fmt.Errorf("selfcheck: set unknown: %v", err)
	}
	if err := eng.Set("d", 1); !errors.Is(err, mat.ErrSetDerived) {
		return fmt.Errorf("selfcheck: set derived: %v", err)
	}
	badDep := dep.Spec{Base: map[string]int{"a": 1}, Derived: map[string]dep.Derived{
		"x": {Deps: []string{"y"}, Fn: func(v []int) int { return v[0] }}}}
	if _, err := New(badDep); !errors.Is(err, dep.ErrUnknownDep) {
		return fmt.Errorf("selfcheck: unknown dep: %v", err)
	}
	cyc := dep.Spec{Derived: map[string]dep.Derived{
		"x": {Deps: []string{"y"}, Fn: func(v []int) int { return v[0] }},
		"y": {Deps: []string{"x"}, Fn: func(v []int) int { return v[0] }}}}
	if _, err := New(cyc); !errors.Is(err, dep.ErrCycle) {
		return fmt.Errorf("selfcheck: cycle: %v", err)
	}
	for _, c := range cols {
		if v, _ := eng.Get(c); v != before[c] {
			return fmt.Errorf("selfcheck: state changed after rejected ops: %s", c)
		}
	}
	return nil
}
