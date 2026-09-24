// Package api 是对外门面：事务登记、依赖排序回放、状态查询与自检。
// 所有方法可并发调用（内部加锁）。
package api

import (
	"errors"
	"fmt"
	"reflect"
	"sync"

	"ontology/gph"
	"ontology/rpl"
)

// 可判定的哨兵错误，五者互不相同。
var (
	ErrInvalidID = gph.ErrInvalidID
	ErrSelfDep   = gph.ErrSelfDep
	ErrDuplicate = gph.ErrDuplicate
	ErrCycle     = gph.ErrCycle
	ErrFull      = gph.ErrFull
)

type Engine struct {
	mu sync.Mutex
	r  *rpl.Replayer
}

func New(maxTxns int) *Engine {
	return &Engine{r: rpl.New(gph.New(maxTxns))}
}

func (e *Engine) Commit(id int, deps ...int) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.r.Commit(id, deps)
}

func (e *Engine) Replay() []int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.r.Replay()
}

func (e *Engine) Replayed() map[int]bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.r.Replayed()
}

func (e *Engine) Staged() []int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.r.Staged()
}

// SelfCheck 对一组内置 Commit/Replay 序列核验四条不变量，全部通过返回 nil。
// 只使用内部新建实例，不影响接收者状态，可并发调用。
func (e *Engine) SelfCheck() error {
	// 不变量 1+2+3：八步序列的集合与回放顺序必须与推导表一致。
	eng := New(16)
	steps := []struct {
		commit  bool
		id      int
		deps    []int
		wantRep []int
		staged  int
	}{
		{true, 3, []int{5}, nil, 1},
		{true, 5, nil, nil, 0},
		{true, 1, []int{5}, nil, 0},
		{false, 0, nil, []int{5, 1, 3}, 0},
		{true, 2, []int{1}, nil, 0},
		{false, 0, nil, []int{2}, 0},
		{true, 7, []int{2}, nil, 0},
		{false, 0, nil, []int{7}, 0},
	}
	for i, s := range steps {
		if s.commit {
			if err := eng.Commit(s.id, s.deps...); err != nil {
				return fmt.Errorf("selfcheck step %d: %w", i+1, err)
			}
		} else if got := eng.Replay(); !reflect.DeepEqual(got, s.wantRep) {
			return fmt.Errorf("selfcheck step %d: replay %v != %v", i+1, got, s.wantRep)
		}
		if got := len(eng.Staged()); got != s.staged {
			return fmt.Errorf("selfcheck step %d: staged %d != %d", i+1, got, s.staged)
		}
	}
	if !eng.Replayed()[1] || !eng.Replayed()[7] || len(eng.Replayed()) != 5 {
		return errors.New("selfcheck: replayed set mismatch")
	}
	// 不变量 4：四类拒绝均可判定且不留痕（含激活暂存事务引发的环）。
	before := eng.Replayed()
	stagedBefore := eng.Staged()
	if err := eng.Commit(0); !errors.Is(err, ErrInvalidID) {
		return errors.New("selfcheck: invalid id not rejected")
	}
	if err := eng.Commit(9, 9); !errors.Is(err, ErrSelfDep) {
		return errors.New("selfcheck: self dep not rejected")
	}
	if err := eng.Commit(1); !errors.Is(err, ErrDuplicate) {
		return errors.New("selfcheck: duplicate not rejected")
	}
	if err := eng.Commit(8, 9); err != nil { // 9 未登记 -> 8 暂存
		return fmt.Errorf("selfcheck: %w", err)
	}
	if err := eng.Commit(9, 8); !errors.Is(err, ErrCycle) { // 激活 8 则 8<->9 成环
		return errors.New("selfcheck: cycle not rejected")
	}
	if !reflect.DeepEqual(before, eng.Replayed()) ||
		!reflect.DeepEqual(append(stagedBefore, 8), eng.Staged()) {
		return errors.New("selfcheck: rejected op changed state")
	}
	if err := New(1).Commit(1); err != nil {
		return fmt.Errorf("selfcheck: %w", err)
	}
	full := New(1)
	full.Commit(1)
	if err := full.Commit(2); !errors.Is(err, ErrFull) {
		return errors.New("selfcheck: overflow not rejected")
	}
	return nil
}
