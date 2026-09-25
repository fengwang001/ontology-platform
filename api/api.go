// Package api 是延迟物化引擎的对外入口：New/Get/Set/SelfCheck。
package api

import (
	"errors"
	"fmt"

	"ontology/dep"
	"ontology/mat"
)

type Table struct{ e *mat.Engine }

// 对外可判定的哨兵错误（构建期错误直接用 dep 包哨兵）。
var (
	ErrUnknown    = mat.ErrUnknownColumn
	ErrSetDerived = mat.ErrSetDerived
)

// New 校验声明并创建表；含环、依赖未声明列等情况整体失败，返回可判定错误。
func New(spec dep.Spec) (*Table, error) {
	g, err := dep.Build(spec)
	if err != nil {
		return nil, err
	}
	return &Table{e: mat.New(g)}, nil
}

func (t *Table) Get(name string) (int, error)   { return t.e.Get(name) }
func (t *Table) Set(name string, val int) error { return t.e.Set(name, val) }

// SelfCheck 用内置操作核验四条不变量；只操作内部私有表、不改接收者状态，
// 因此可被多个 goroutine 并发调用。全部通过返回 nil。
func (t *Table) SelfCheck() error {
	sum, dbl := func(v []int) int { return v[0] + v[1] }, func(v []int) int { return v[0] * 2 }
	calls := map[string]int{}
	prof := func(k string, f func([]int) int) func([]int) int {
		return func(v []int) int { calls[k]++; return f(v) }
	}
	g, err := dep.Build(dep.Spec{Columns: []dep.Column{
		{Name: "a", Base: true, Initial: 2}, {Name: "b", Base: true, Initial: 3},
		{Name: "c", Base: true, Initial: 4},
		{Name: "d", Deps: []string{"a", "b"}, Fn: prof("d", sum)},
		{Name: "e", Deps: []string{"d"}, Fn: prof("e", dbl)},
		{Name: "f", Deps: []string{"c", "e"}, Fn: prof("f", sum)},
	}})
	if err != nil {
		return err
	}
	x := mat.New(g)
	bases := map[string]int{"a": 2, "b": 3, "c": 4}
	raw := map[string]func([]int) int{"d": sum, "e": dbl, "f": sum}
	var naive func(string) int // 朴素参照：未计数原始函数从头即时重算
	naive = func(name string) int {
		f, ok := raw[name]
		if !ok {
			return bases[name]
		}
		vs := make([]int, 0, 2)
		for _, d := range g.Deps(name) {
			vs = append(vs, naive(d))
		}
		return f(vs)
	}
	totalCalls := func() int { return calls["d"] + calls["e"] + calls["f"] }
	// 每步：Get 列、期望值、截至该步累计 Fn 调用次数（2,2,3 |Set| 4,6,6）。
	steps := []struct {
		get string
		v   int
		c   int
	}{
		{"e", 10, 2}, {"d", 5, 2}, {"f", 14, 3},
		{"d", 8, 4}, {"f", 20, 6}, {"e", 16, 6},
	}
	check := func(i int) error {
		s := steps[i]
		v, e := x.Get(s.get)
		if e != nil || v != s.v || totalCalls() != s.c {
			return fmt.Errorf("step %d Get(%s)=%d,%v calls=%d want %d,%d", i+1, s.get, v, e, totalCalls(), s.v, s.c)
		}
		return nil
	}
	// 全列读一遍逐列对照朴素值；strict 时该轮还不得触发任何 Fn。
	matchNaive := func(strict bool) error {
		before := totalCalls()
		for _, n := range g.Names() {
			got, e := x.Get(n)
			if e != nil || got != naive(n) {
				return fmt.Errorf("%s=%d,%v want naive %d", n, got, e, naive(n))
			}
		}
		if strict && totalCalls() != before {
			return fmt.Errorf("unexpected recompute %v", calls)
		}
		return nil
	}
	for i := 0; i < 3; i++ { // 前三步
		if err := check(i); err != nil {
			return err
		}
	}
	if err := matchNaive(true); err != nil {
		return err // 全缓存：值一致且零重算
	}
	if err := x.Set("a", 5); err != nil {
		return err
	}
	bases["a"] = 5
	for i := 3; i < 6; i++ { // 后三步
		if err := check(i); err != nil {
			return err
		}
	}
	if err := checkRejected(x); err != nil {
		return err
	}
	return matchNaive(true) // 被拒操作后值一致、计数不增长（状态/缓存均未变）
}

// checkRejected 核验四类错误互不相同，且被拒后实例状态不变。
func checkRejected(x *mat.Engine) error {
	if _, e := x.Get("nope"); !errors.Is(e, mat.ErrUnknownColumn) {
		return fmt.Errorf("Get unknown: %w", e)
	}
	if e := x.Set("nope", 1); !errors.Is(e, mat.ErrUnknownColumn) {
		return fmt.Errorf("Set unknown: %w", e)
	}
	if e := x.Set("d", 9); !errors.Is(e, mat.ErrSetDerived) {
		return fmt.Errorf("Set derived: %w", e)
	}
	id := func(v []int) int { return v[0] }
	mk := func(n string, ds ...string) dep.Column { return dep.Column{Name: n, Deps: ds, Fn: id} }
	b := dep.Column{Name: "a", Base: true}
	if _, e := New(dep.Spec{Columns: []dep.Column{b, mk("x", "y"), mk("y", "x")}}); !errors.Is(e, dep.ErrCyclic) {
		return fmt.Errorf("cyclic not rejected: %w", e)
	}
	if _, e := New(dep.Spec{Columns: []dep.Column{b, mk("x", "zzz")}}); !errors.Is(e, dep.ErrUndeclaredDep) {
		return fmt.Errorf("undeclared dep not rejected: %w", e)
	}
	return nil
}
