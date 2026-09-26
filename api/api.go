// Package api 对外暴露注册表操作与自检。依赖 reg。
package api

import (
	"errors"
	"fmt"

	"ontology/reg"
	"ontology/vv"
)

// 三类可判定哨兵错误（再导出，值与 reg 中相同）。
var (
	ErrNegativeCounter = reg.ErrNegativeCounter
	ErrNegativeActor   = reg.ErrNegativeActor
	ErrUnknownName     = reg.ErrUnknownName
)

// API 是对外的薄包装。
type API struct {
	r *reg.Registry
}

// New 创建 API 实例。
func New() *API {
	return &API{r: reg.New()}
}

// Set 注册/更新副本向量。
func (a *API) Set(name string, v vv.Vector) error {
	return a.r.Set(name, v)
}

// Merge 合并两个副本的向量。
func (a *API) Merge(nameA, nameB string) (vv.Vector, error) {
	return a.r.Merge(nameA, nameB)
}

// Compare 比较两个副本向量的因果关系。
func (a *API) Compare(nameA, nameB string) (vv.Order, error) {
	return a.r.Compare(nameA, nameB)
}

// naive 是合并的朴素重算：枚举并集所有 key、逐 key 取 max。
func naive(a, b vv.Vector) vv.Vector {
	keys := map[int]bool{}
	for k := range a {
		keys[k] = true
	}
	for k := range b {
		keys[k] = true
	}
	out := vv.Vector{}
	for k := range keys {
		if a[k] > b[k] {
			out[k] = a[k]
		} else {
			out[k] = b[k]
		}
	}
	return out
}

func sameMap(a, b vv.Vector) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if bv, ok := b[k]; !ok || bv != v {
			return false
		}
	}
	return true
}

// SelfCheck 对内置操作序列核验四条不变量，全部通过返回 nil。
func (a *API) SelfCheck() error {
	for _, s := range []struct {
		name string
		v    vv.Vector
	}{
		{"r1", vv.Vector{0: 1}}, {"r2", vv.Vector{0: 1, 1: 2}}, {"r3", vv.Vector{1: 1, 2: 1}},
	} {
		if err := a.Set(s.name, s.v); err != nil {
			return fmt.Errorf("selfcheck set %s: %w", s.name, err)
		}
	}
	m12, err := a.Merge("r1", "r2")
	if err != nil {
		return err
	}
	// 不变量 1：与朴素重算一致。
	if !sameMap(m12, naive(vv.Vector{0: 1}, vv.Vector{0: 1, 1: 2})) {
		return errors.New("selfcheck: merge differs from naive recomputation")
	}
	// 不变量 2：交换律、幂等、结果 >= 两个输入。
	m21, _ := a.Merge("r2", "r1")
	m11, _ := a.Merge("r1", "r1")
	if !sameMap(m12, m21) || !sameMap(m11, vv.Vector{0: 1}) {
		return errors.New("selfcheck: merge is not commutative/idempotent")
	}
	if c1, _ := a.Compare("r1", "r2"); c1 != vv.Less {
		return errors.New("selfcheck: merged r2 must dominate r1")
	}
	// 不变量 3：三歧比较与逐 key 定义一致。
	if c, _ := a.Compare("r1", "r3"); c != vv.Concurrent {
		return errors.New("selfcheck: r1 and r3 must be concurrent")
	}
	if c, _ := a.Compare("r1", "r1"); c != vv.Equal {
		return errors.New("selfcheck: r1 must equal itself")
	}
	// 不变量 4：三类拒绝互不相同且不留痕。
	e1 := a.Set("bad", vv.Vector{0: -1})
	e2 := a.Set("bad", vv.Vector{-1: 0})
	_, e3 := a.Merge("r1", "ghost")
	if !errors.Is(e1, ErrNegativeCounter) || !errors.Is(e2, ErrNegativeActor) ||
		!errors.Is(e3, ErrUnknownName) || e1 == e2 || e2 == e3 || e1 == e3 {
		return errors.New("selfcheck: sentinel errors are not distinct/decidable")
	}
	again, _ := a.Merge("r1", "r2")
	if !sameMap(again, m12) {
		return errors.New("selfcheck: rejected operation mutated registry")
	}
	return nil
}
