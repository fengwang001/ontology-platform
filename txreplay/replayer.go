// Package txreplay 提供一个带依赖排序的事务日志回放器。
//
// 事务以正整数标识，登记时声明依赖集合。依赖尚未登记的事务进入暂存集；
// 当其全部依赖都已登记且已回放后，事务自动激活。Replay 反复选取当前可回放
// 事务中标识最小者回放，因此同一组登记与依赖关系产生的回放序列始终一致。
package txreplay

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// 默认事务数上限，可通过 WithMaxTransactions 调整。
const DefaultMaxTransactions = 1024

// 可区分的拒绝原因。调用方可使用 errors.Is 判定具体类别。
var (
	// ErrInvalidID 表示事务标识非法（非正整数）或为空。
	ErrInvalidID = errors.New("txreplay: invalid or empty transaction id")
	// ErrSelfDependency 表示事务把自身声明为依赖。
	ErrSelfDependency = errors.New("txreplay: transaction depends on itself")
	// ErrDuplicateRegistration 表示同一事务被重复登记。
	ErrDuplicateRegistration = errors.New("txreplay: duplicate transaction registration")
	// ErrCycleDetected 表示新增依赖会使依赖图出现环。
	ErrCycleDetected = errors.New("txreplay: dependency cycle detected")
	// ErrTooManyTransactions 表示登记后事务数量超过上限。
	ErrTooManyTransactions = errors.New("txreplay: transaction limit exceeded")
)

// Replayer 是带依赖排序的事务日志回放器。
// 单个 Replayer 的所有方法均支持并发调用。
type Replayer struct {
	mu sync.RWMutex
	// 事务数上限（含已登记但暂存的事务）。
	max int
	// registered 记录每个已登记事务声明的去重依赖集合。
	registered map[int]map[int]struct{}
	// replayedOrder 按回放顺序记录已回放事务。
	replayedOrder []int
	replayed      map[int]struct{}
	// ready 为依赖已全部回放、等待回放的事务。
	ready map[int]struct{}
	// pending 为仍有依赖未登记或未回放的暂存事务。
	pending map[int]struct{}
	// remaining[id] 为事务 id 尚未回放的依赖数量；为 0 时即激活。
	remaining map[int]int
	// dependents[x] 为依赖了 x 的事务集合，x 回放后用于激活后继。
	dependents map[int]map[int]struct{}
}

// Option 配置 Replayer。
type Option func(*Replayer)

// WithMaxTransactions 设置事务数上限（必须为正数）。
func WithMaxTransactions(n int) Option {
	return func(r *Replayer) {
		if n > 0 {
			r.max = n
		}
	}
}

// New 创建一个使用默认上限的回放器。
func New(opts ...Option) *Replayer {
	r := &Replayer{
		max:        DefaultMaxTransactions,
		registered: make(map[int]map[int]struct{}),
		replayed:   make(map[int]struct{}),
		ready:      make(map[int]struct{}),
		pending:    make(map[int]struct{}),
		remaining:  make(map[int]int),
		dependents: make(map[int]map[int]struct{}),
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// Register 登记一个事务及其依赖集合。
//
// 非法登记会被整体拒绝，依赖图、暂存集与已回放集均不发生变化。
func (r *Replayer) Register(id int, deps ...int) error {
	if id <= 0 {
		return ErrInvalidID
	}
	depSet := make(map[int]struct{}, len(deps))
	for _, dep := range deps {
		if dep <= 0 {
			return fmt.Errorf("%w: dependency %d of transaction %d", ErrInvalidID, dep, id)
		}
		if dep == id {
			return fmt.Errorf("%w: transaction %d", ErrSelfDependency, id)
		}
		depSet[dep] = struct{}{}
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.registered[id]; ok {
		return fmt.Errorf("%w: transaction %d", ErrDuplicateRegistration, id)
	}
	// 环检测：只有当某个已登记依赖经由依赖边能够到达 id 时才成环。
	// 未登记依赖无法提供任何路径，故只需在已登记子图中搜索。
	if r.pathToID(depSet, id) {
		return fmt.Errorf("%w: transaction %d closes a cycle", ErrCycleDetected, id)
	}
	if len(r.registered) >= r.max {
		return fmt.Errorf("%w: limit %d", ErrTooManyTransactions, r.max)
	}

	// 全部校验通过后才落盘，保证一次失败不改变任何内部状态。
	r.registered[id] = depSet

	unreplayed := 0
	for dep := range depSet {
		if _, done := r.replayed[dep]; !done {
			unreplayed++
			waiters := r.dependents[dep]
			if waiters == nil {
				waiters = make(map[int]struct{})
				r.dependents[dep] = waiters
			}
			waiters[id] = struct{}{}
		}
	}
	r.remaining[id] = unreplayed
	if unreplayed == 0 {
		r.ready[id] = struct{}{}
	} else {
		r.pending[id] = struct{}{}
	}
	return nil
}

// pathToID 在已登记依赖图中判断：从 starts 中任一节点出发，
// 是否存在一条指向 target 的依赖路径（target 本身尚未登记）。
func (r *Replayer) pathToID(starts map[int]struct{}, target int) bool {
	stack := make([]int, 0, len(starts))
	for dep := range starts {
		if dep == target {
			return true
		}
		if _, ok := r.registered[dep]; ok {
			stack = append(stack, dep)
		}
	}
	visited := make(map[int]struct{}, len(starts))
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if _, seen := visited[cur]; seen {
			continue
		}
		visited[cur] = struct{}{}
		for next := range r.registered[cur] {
			if next == target {
				return true
			}
			if _, ok := r.registered[next]; ok {
				if _, seen := visited[next]; !seen {
					stack = append(stack, next)
				}
			}
		}
	}
	return false
}

// Replay 反复回放当前可回放事务中标识最小者，直到没有可回放事务，
// 返回本次调用新回放的事务标识序列（按回放顺序）。
func (r *Replayer) Replay() []int {
	r.mu.Lock()
	defer r.mu.Unlock()

	played := make([]int, 0)
	for {
		if len(r.ready) == 0 {
			return played
		}
		id := minKey(r.ready)
		delete(r.ready, id)
		r.replayed[id] = struct{}{}
		r.replayedOrder = append(r.replayedOrder, id)
		played = append(played, id)

		// 激活因 id 而等待的暂存事务。
		for next := range r.dependents[id] {
			r.remaining[next]--
			if r.remaining[next] == 0 {
				delete(r.pending, next)
				r.ready[next] = struct{}{}
			}
		}
		delete(r.dependents, id)
	}
}

// minKey 返回集合中的最小标识。
func minKey(set map[int]struct{}) int {
	first := true
	min := 0
	for k := range set {
		if first || k < min {
			min, first = k, false
		}
	}
	return min
}

// Replayed 返回迄今为止已回放的事务标识，按回放顺序排列。
func (r *Replayer) Replayed() []int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]int(nil), r.replayedOrder...)
}

// Pending 返回仍处于暂存状态（依赖未全部就绪）的事务标识，按升序排列。
func (r *Replayer) Pending() []int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return sortedKeys(r.pending)
}

// Ready 返回已激活（全部依赖已回放）但尚未回放的事务标识，按升序排列。
func (r *Replayer) Ready() []int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return sortedKeys(r.ready)
}

func sortedKeys(set map[int]struct{}) []int {
	out := make([]int, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Ints(out)
	return out
}

// IsTopologicalOrder 校验 order 是否为 deps 描述的依赖图的合法拓扑序：
// 每个事务在序列中都晚于其全部依赖。deps 将事务标识映射到其依赖标识列表，
// 未在 deps 中出现的依赖视为没有进一步前驱。
//
// 该函数供本地验证回放结果使用（见 README）。
func IsTopologicalOrder(order []int, deps map[int][]int) bool {
	pos := make(map[int]int, len(order))
	for i, id := range order {
		if _, dup := pos[id]; dup {
			return false
		}
		pos[id] = i
	}
	for id, ds := range deps {
		idPos, ok := pos[id]
		if !ok {
			continue
		}
		for _, dep := range ds {
			depPos, ok := pos[dep]
			if !ok {
				// 成环或缺失依赖时，相关事务不会出现在回放序列中。
				continue
			}
			if depPos >= idPos {
				return false
			}
		}
	}
	return true
}
