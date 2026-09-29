// Package replay 提供带依赖排序的事务日志回放器。
//
// 事务以正整数标识并声明依赖集合。依赖未全部登记的事务进入暂存集，
// 依赖齐后自动激活；回放时反复选取可回放事务中标识最小者，
// 保证依赖先行且同一组登记下回放序列逐次相同。
package replay

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// wrap 为哨兵错误附加上下文，保持 errors.Is 可判定。
func wrap(err error, format string, args ...any) error {
	return fmt.Errorf("%w: %s", err, fmt.Sprintf(format, args...))
}

// 可区分的失败原因，调用方可用 errors.Is 判定。
var (
	// ErrInvalidID 标识为非正整数或依赖含非正整数。
	ErrInvalidID = errors.New("replay: invalid transaction id")
	// ErrSelfDependency 事务依赖自身。
	ErrSelfDependency = errors.New("replay: self dependency")
	// ErrDuplicate 事务标识重复登记。
	ErrDuplicate = errors.New("replay: duplicate transaction")
	// ErrCycle 登记会在依赖图中成环。
	ErrCycle = errors.New("replay: dependency cycle")
	// ErrCapacityExceeded 超出事务数上限。
	ErrCapacityExceeded = errors.New("replay: capacity exceeded")
)

// transaction 是已登记事务的内部表示。
type transaction struct {
	id   int64
	deps map[int64]struct{}
}

// Replayer 登记事务并按依赖顺序回放，所有方法可并发调用。
type Replayer struct {
	mu       sync.RWMutex
	max      int
	txs      map[int64]*transaction // 全部已登记事务（含暂存）
	staged   map[int64]struct{}     // 依赖未齐、等待激活的暂存集
	replayed map[int64]struct{}     // 已回放集
}

// NewReplayer 创建回放器，max 为可登记事务数上限（<=0 表示不限）。
func NewReplayer(max int) *Replayer {
	return &Replayer{
		max:      max,
		txs:      make(map[int64]*transaction),
		staged:   make(map[int64]struct{}),
		replayed: make(map[int64]struct{}),
	}
}

// Register 登记事务及其依赖。任一校验失败则整体拒绝，不改变任何状态。
func (r *Replayer) Register(id int64, deps []int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if id <= 0 {
		return wrap(ErrInvalidID, "id=%d 必须为正整数", id)
	}
	depSet := make(map[int64]struct{}, len(deps))
	for _, d := range deps {
		if d <= 0 {
			return wrap(ErrInvalidID, "id=%d 的依赖 %d 必须为正整数", id, d)
		}
		if d == id {
			return wrap(ErrSelfDependency, "id=%d 依赖自身", id)
		}
		depSet[d] = struct{}{}
	}
	if _, ok := r.txs[id]; ok {
		return wrap(ErrDuplicate, "id=%d 已登记", id)
	}
	if r.max > 0 && len(r.txs) >= r.max {
		return wrap(ErrCapacityExceeded, "id=%d 超出上限 %d", id, r.max)
	}
	if r.reachesID(depSet, id) {
		return wrap(ErrCycle, "id=%d 与既有依赖关系成环", id)
	}

	// 校验全部通过后才变更状态，保证失败不影响依赖图、暂存集与已回放集。
	r.txs[id] = &transaction{id: id, deps: depSet}
	if !r.allRegistered(depSet) {
		r.staged[id] = struct{}{}
	}
	r.activateReady()
	return nil
}

// allRegistered 报告依赖集合是否全部已登记。
func (r *Replayer) allRegistered(deps map[int64]struct{}) bool {
	for d := range deps {
		if _, ok := r.txs[d]; !ok {
			return false
		}
	}
	return true
}

// reachesID 沿既有依赖边做 DFS，报告从 deps 出发能否到达 id，
// 用于在登记前检测新边是否会成环。
func (r *Replayer) reachesID(deps map[int64]struct{}, id int64) bool {
	visited := make(map[int64]struct{}, len(r.txs))
	stack := make([]int64, 0, len(deps))
	for d := range deps {
		stack = append(stack, d)
	}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if cur == id {
			return true
		}
		if _, ok := visited[cur]; ok {
			continue
		}
		visited[cur] = struct{}{}
		if tx, ok := r.txs[cur]; ok {
			for next := range tx.deps {
				stack = append(stack, next)
			}
		}
	}
	return false
}

// activateReady 将依赖已全部登记的暂存事务移出暂存集。
func (r *Replayer) activateReady() {
	for sid := range r.staged {
		if r.allRegistered(r.txs[sid].deps) {
			delete(r.staged, sid)
		}
	}
}

// Replay 反复选取可回放事务中标识最小者回放，直到无可用事务，
// 返回本次新回放的事务标识序列（升序选取、依赖先行）。
func (r *Replayer) Replay() []int64 {
	r.mu.Lock()
	defer r.mu.Unlock()

	var seq []int64
	for {
		next, ok := r.smallestReady()
		if !ok {
			return seq
		}
		r.replayed[next] = struct{}{}
		seq = append(seq, next)
	}
}

// smallestReady 返回未回放、未暂存且依赖全部已回放的最小标识。
func (r *Replayer) smallestReady() (int64, bool) {
	best := int64(0)
	found := false
	for id, tx := range r.txs {
		if _, ok := r.staged[id]; ok {
			continue
		}
		if _, ok := r.replayed[id]; ok {
			continue
		}
		if !r.allReplayed(tx.deps) {
			continue
		}
		if !found || id < best {
			best = id
			found = true
		}
	}
	return best, found
}

// allReplayed 报告依赖集合是否全部已回放。
func (r *Replayer) allReplayed(deps map[int64]struct{}) bool {
	for d := range deps {
		if _, ok := r.replayed[d]; !ok {
			return false
		}
	}
	return true
}

// Replayed 返回已回放事务标识的升序快照，可并发调用。
func (r *Replayer) Replayed() []int64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return sortedKeys(r.replayed)
}

// Pending 返回暂存集中事务标识的升序快照，可并发调用。
func (r *Replayer) Pending() []int64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return sortedKeys(r.staged)
}

// IsReplayed 报告事务是否已回放，可并发调用。
func (r *Replayer) IsReplayed(id int64) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.replayed[id]
	return ok
}

// IsPending 报告事务是否在暂存集中，可并发调用。
func (r *Replayer) IsPending(id int64) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.staged[id]
	return ok
}

// sortedKeys 返回集合键的升序切片。
func sortedKeys(set map[int64]struct{}) []int64 {
	keys := make([]int64, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	return keys
}
