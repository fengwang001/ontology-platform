// Package api 是对外面：并发安全的并集维护句柄与自检。依赖 union。
package api

import (
	"errors"
	"fmt"
	"slices"
	"sync"

	"ontology/seg"
	"ontology/union"
)

// 三类可判定哨兵错误，互不相同。
var (
	ErrInvalidInterval  = union.ErrInvalidInterval
	ErrTooManyIntervals = union.ErrTooManyIntervals
	ErrNotFound         = union.ErrNotFound
)

// Interval 是左闭右开区间 [S,E)；Change 是一条变更日志（Del 为 '-'）。
type (
	Interval = seg.Interval
	Change   = union.Change
)

// API 是并发安全的并集维护句柄。
type API struct {
	mu sync.RWMutex
	u  *union.Union
}

// New 返回空并集，maxIntervals 为段数上限（仅约束 Add）。
func New(maxIntervals int) *API { return &API{u: union.New(maxIntervals)} }

// Add 把 [s,e) 并入并集，Withdraw 从并集撤掉 [s,e)，均返回本次变更日志。
func (a *API) Add(s, e int64) ([]Change, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.u.Add(s, e)
}

func (a *API) Withdraw(s, e int64) ([]Change, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.u.Withdraw(s, e)
}

// View 返回当前并集的最大不相交分段。
func (a *API) View() []Interval {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.u.View()
}

// SelfCheck 在内置操作序列上核验四条不变量，全部通过返回 nil。
func (a *API) SelfCheck() error {
	ops := selfcheckOps()
	u := union.New(16)
	var shadow []Interval
	for i, o := range ops {
		var ch []Change
		var err error
		if o.add {
			ch, err = u.Add(o.s, o.e)
		} else {
			ch, err = u.Withdraw(o.s, o.e)
		}
		if err != nil {
			return fmt.Errorf("selfcheck op %d: %w", i, err)
		}
		for _, c := range ch { // 不变量 2：'-' 必须精确命中现有段
			ok := false
			if shadow, ok = union.ApplyChange(shadow, c); !ok {
				return fmt.Errorf("selfcheck op %d: bad changelog", i)
			}
		}
		if got := u.View(); !slices.Equal(got, naiveReplay(ops[:i+1])) || // 不变量 1
			!slices.Equal(shadow, got) || !seg.Maximal(got) { // 不变量 3
			return fmt.Errorf("selfcheck op %d: invariant violated", i)
		}
	}
	// 不变量 4：失败不留痕；三类错误互不相同；被拒后仍可用。
	u2 := union.New(2)
	u2.Add(0, 5)
	u2.Add(10, 15)
	before, logN := u2.View(), len(u2.Log())
	_, e1 := u2.Add(3, 3)
	_, e3 := u2.Add(20, 25)
	_, e4 := u2.Withdraw(6, 8)
	if !errors.Is(e1, ErrInvalidInterval) || !errors.Is(e3, ErrTooManyIntervals) ||
		!errors.Is(e4, ErrNotFound) {
		return fmt.Errorf("selfcheck: sentinel mismatch: %v %v %v", e1, e3, e4)
	}
	if errors.Is(ErrInvalidInterval, ErrTooManyIntervals) ||
		errors.Is(ErrTooManyIntervals, ErrNotFound) || errors.Is(ErrInvalidInterval, ErrNotFound) {
		return errors.New("selfcheck: sentinel errors not distinct")
	}
	if !slices.Equal(u2.View(), before) || len(u2.Log()) != logN {
		return errors.New("selfcheck: rejected op changed state")
	}
	if _, err := u2.Add(5, 10); err != nil {
		return fmt.Errorf("selfcheck: unusable after rejection: %w", err)
	}
	return nil
}

type op struct {
	add  bool
	s, e int64
}

// selfcheckOps 返回内置序列：题目八步 + 确定性生成的铺开/撤回/合并序列。
func selfcheckOps() []op {
	ops := []op{{true, 0, 10}, {true, 10, 20}, {true, 30, 40}, {false, 15, 35},
		{true, 15, 35}, {false, 20, 25}, {true, 20, 25}, {false, 0, 40}}
	for i := int64(0); i < 8; i++ {
		ops = append(ops, op{true, i * 10, i*10 + 6})
		if i%2 == 0 {
			ops = append(ops, op{false, i*10 + 2, i*10 + 4})
		}
	}
	return append(ops, op{true, 6, 12}, op{true, 18, 30}, op{false, 0, 80})
}

// naiveReplay 朴素参照：每步用 seg 的整体重算重放同一序列。
func naiveReplay(ops []op) []Interval {
	var segs []Interval
	for _, o := range ops {
		if o.add {
			segs = seg.NaiveAdd(segs, o.s, o.e)
		} else {
			segs = seg.NaiveWithdraw(segs, o.s, o.e)
		}
	}
	return segs
}
