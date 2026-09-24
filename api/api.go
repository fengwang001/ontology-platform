// Package api 对外提供区间并集增量维护接口。依赖 union。
package api

import (
	"cmp"
	"errors"
	"fmt"
	"slices"

	"ontology/union"
)

type (
	Interval = union.Interval // 并集视图中的一段 [S,E)
	Change   = union.Change   // 变更日志条目：Del=true 为 -[S,E)，否则为 +[S,E)
)

var (
	ErrInvalid  = union.ErrInvalid  // 区间非法 s >= e
	ErrTooMany  = union.ErrTooMany  // Add 后段数超过 maxIntervals
	ErrNotFound = union.ErrNotFound // Withdraw 找不到对象
)

// Engine 是并发安全的并集维护器。
type Engine struct{ u *union.Set }

// New 创建空并集，段数上限 maxIntervals。
func New(maxIntervals int) *Engine { return &Engine{u: union.New(maxIntervals)} }

func (e *Engine) Add(s, e2 int64) ([]Change, error)      { return e.u.Add(s, e2) }
func (e *Engine) Withdraw(s, e2 int64) ([]Change, error) { return e.u.Withdraw(s, e2) }
func (e *Engine) View() []Interval                       { return e.u.View() } // 可并发调用

func byS(a, b Interval) int { return cmp.Compare(a.S, b.S) }

var fixed = [8][3]int64{{0, 0, 10}, {0, 10, 20}, {0, 30, 40}, {1, 15, 35}, {0, 15, 35}, {1, 20, 25}, {0, 20, 25}, {1, 0, 40}}

func run(e *Engine, op, s, w int64) ([]Change, error) {
	if op == 0 {
		return e.Add(s, w)
	}
	return e.Withdraw(s, w)
}

// SelfCheck 对内置序列（八步 + 随机派生）核验四条不变量，可并发调用。
func (e *Engine) SelfCheck() error {
	rng := int64(12345)
	for trial := 0; trial < 4; trial++ {
		cur := New(12)
		var naive, down []Interval
		for step := 0; step < 8; step++ {
			op, s, w := fixed[step][0], fixed[step][1], fixed[step][2]
			if trial > 0 { // 派生随机序列，含随机到达顺序
				rng = rng*6364136223846793005 + 1442695040888963407
				s, w, op = min(rng%40, rng>>16%40), max(rng%40, rng>>16%40)+1, rng>>32&1
			}
			before := cur.View()
			log, err := run(cur, op, s, w)
			if err != nil { // 不变量4：被拒后状态不变，错误可判定
				okErr := op == 0 && errors.Is(err, ErrTooMany) || op == 1 && errors.Is(err, ErrNotFound)
				if !okErr || !slices.Equal(before, cur.View()) {
					return fmt.Errorf("selfcheck: reject violated: err=%v", err)
				}
				continue
			}
			if op == 0 {
				naive = naiveAdd(naive, s, w)
			} else if naive = naiveWithdraw(naive, s, w); naive == nil {
				return fmt.Errorf("selfcheck: withdraw should miss")
			}
			if !slices.Equal(cur.View(), naive) || !maximal(naive) { // 不变量1、3
				return fmt.Errorf("selfcheck: view != naive or not maximal")
			}
			if err := applyLog(&down, log); err != nil { // 不变量2
				return err
			}
			down = slices.SortedFunc(slices.Values(down), byS)
			if !slices.Equal(down, cur.View()) {
				return fmt.Errorf("selfcheck: downstream diverged")
			}
		}
	}
	return nil
}

// maximal 判定归并完备：相邻段满足 next.S > prev.E（不变量3）。
func maximal(v []Interval) bool {
	for k := 1; k < len(v); k++ {
		if v[k].S <= v[k-1].E {
			return false
		}
	}
	return true
}

// applyLog 模拟下游按顺序应用日志：每条 - 必须精确命中当前段（不变量2）。
func applyLog(cur *[]Interval, log []Change) error {
	for _, c := range log {
		if !c.Del {
			*cur = append(*cur, c.I)
			continue
		}
		k := slices.Index(*cur, c.I)
		if k < 0 {
			return fmt.Errorf("selfcheck: del of absent %v", c.I)
		}
		*cur = slices.Delete(*cur, k, k+1)
	}
	return nil
}

// naiveAdd/naiveWithdraw 是朴素参照：整体重算合并/拆分（O(n²)），withdraw 无重叠返回 nil。
func naiveAdd(u []Interval, s, e int64) []Interval {
	u = append(slices.Clone(u), Interval{S: s, E: e})
	for changed := true; changed; {
		changed = false
		for i := 0; i < len(u) && !changed; i++ {
			for j := i + 1; j < len(u); j++ {
				if a, b := u[i], u[j]; a.E >= b.S && a.S <= b.E {
					u[i], u = Interval{S: min(a.S, b.S), E: max(a.E, b.E)}, slices.Delete(u, j, j+1)
					changed = true
					break
				}
			}
		}
	}
	return slices.SortedFunc(slices.Values(u), byS)
}

func naiveWithdraw(u []Interval, s, e int64) []Interval {
	out := make([]Interval, 0, len(u)+1)
	hit := false
	for _, g := range u {
		if g.S < e && s < g.E {
			hit = true
			if g.S < s {
				out = append(out, Interval{S: g.S, E: min(s, g.E)})
			}
			if e < g.E {
				out = append(out, Interval{S: max(e, g.S), E: g.E})
			}
		} else {
			out = append(out, g)
		}
	}
	if !hit {
		return nil
	}
	return out
}
