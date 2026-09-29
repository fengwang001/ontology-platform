// Package median 维护一个支持加入（Add）与撤回（Remove）的整数多重集，
// 使用两个带懒删除的堆动态维护中位数。
package median

import (
	"fmt"
	"sync"
)

// MaxElements 是单个 Tracker 允许保存的不同值（多重集元素）个数上限默认值。
const MaxElements = 1 << 30

// Tracker 是带撤回的增量中位数维护器。零值不可用，请使用 New 创建。
type Tracker struct {
	mu          sync.RWMutex
	lo          *heapSide // 较小一半，大顶堆，保存中间偏下中位数
	hi          *heapSide // 较大一半，小顶堆
	count       map[int]int
	size        int
	maxElements int
}

// New 创建一个使用默认元素上限的 Tracker。
func New() *Tracker {
	t, _ := NewWithMax(MaxElements)
	return t
}

// NewWithMax 创建一个自定义元素上限的 Tracker。
func NewWithMax(maxElements int) (*Tracker, error) {
	if maxElements <= 0 {
		return nil, fmt.Errorf("%w: max elements must be positive, got %d", ErrInvalidArgument, maxElements)
	}
	return &Tracker{
		lo:          newHeapSide(func(a, b int) bool { return a > b }),
		hi:          newHeapSide(func(a, b int) bool { return a < b }),
		count:       make(map[int]int),
		maxElements: maxElements,
	}, nil
}

// Add 向多重集加入一个值。
func (t *Tracker) Add(v int) error { return t.Apply(Op{Kind: OpAdd, V: v}) }

// Remove 从多重集撤回一个值；撤回不存在的值报错且不留痕。
func (t *Tracker) Remove(v int) error { return t.Apply(Op{Kind: OpRemove, V: v}) }

// Median 返回中间偏下位置的中位数；空集报错。
func (t *Tracker) Median() (int, error) {
	if t == nil {
		return 0, fmt.Errorf("%w: nil tracker", ErrInvalidArgument)
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	if t.size == 0 {
		return 0, ErrEmpty
	}
	// 任何一次提交结束时都已清理到堆顶有效，因此读路径无需修改任何结构，
	// 多个 Median/Len/Check 之间以及与提交之间都可以安全并发（-race）。
	return t.lo.top(), nil
}

// Len 返回当前有效元素个数。
func (t *Tracker) Len() int {
	if t == nil {
		return 0
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.size
}

// Check 校验内部不变量，正常返回 nil。
func (t *Tracker) Check() error {
	if t == nil {
		return fmt.Errorf("%w: nil tracker", ErrInvalidArgument)
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.checkLocked()
}

func (t *Tracker) checkLocked() error {
	if t.size != t.lo.valid+t.hi.valid {
		return fmt.Errorf("median: self-check: size %d != lo.valid %d + hi.valid %d",
			t.size, t.lo.valid, t.hi.valid)
	}
	sum := 0
	for _, c := range t.count {
		if c <= 0 {
			return fmt.Errorf("median: self-check: non-positive frequency %d", c)
		}
		sum += c
	}
	if sum != t.size {
		return fmt.Errorf("median: self-check: frequency sum %d != size %d", sum, t.size)
	}
	if t.lo.valid < 0 || t.hi.valid < 0 {
		return fmt.Errorf("median: self-check: negative valid count")
	}
	if d := t.lo.valid - t.hi.valid; d != 0 && d != 1 {
		return fmt.Errorf("median: self-check: balance violated lo %d hi %d", t.lo.valid, t.hi.valid)
	}
	pendingLo := 0
	for _, c := range t.lo.pending {
		pendingLo += c
	}
	pendingHi := 0
	for _, c := range t.hi.pending {
		pendingHi += c
	}
	if t.lo.rawLen() != t.lo.valid+pendingLo {
		return fmt.Errorf("median: self-check: lo raw length inconsistent")
	}
	if t.hi.rawLen() != t.hi.valid+pendingHi {
		return fmt.Errorf("median: self-check: hi raw length inconsistent")
	}
	if !t.lo.heapOrdered() {
		return fmt.Errorf("median: self-check: lo heap order violated")
	}
	if !t.hi.heapOrdered() {
		return fmt.Errorf("median: self-check: hi heap order violated")
	}
	if t.size > 0 {
		if t.lo.valid == 0 || len(t.lo.items) == 0 {
			return fmt.Errorf("median: self-check: non-empty set but lo empty")
		}
		if t.lo.pending[t.lo.items[0]] != 0 {
			return fmt.Errorf("median: self-check: lo top %d is a stale copy", t.lo.items[0])
		}
	}
	if t.hi.valid > 0 {
		if len(t.hi.items) == 0 {
			return fmt.Errorf("median: self-check: hi has valid elements but raw heap empty")
		}
		if t.hi.pending[t.hi.items[0]] != 0 {
			return fmt.Errorf("median: self-check: hi top %d is a stale copy", t.hi.items[0])
		}
		if t.lo.top() > t.hi.top() {
			return fmt.Errorf("median: self-check: lo top %d > hi top %d", t.lo.top(), t.hi.top())
		}
	}
	return nil
}

// Apply 在一次原子提交中依次执行一批操作；任一非法则整批拒绝。
func (t *Tracker) Apply(ops ...Op) error {
	if t == nil {
		return fmt.Errorf("%w: nil tracker", ErrInvalidArgument)
	}
	if len(ops) == 0 {
		return fmt.Errorf("%w: empty operation batch", ErrInvalidArgument)
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	// 先在“虚拟提交”的计数上完成整批校验：任何一项非法都直接拒绝，
	// 此时两个堆与真实计数均未被触碰，失败不留痕。
	sim := make(map[int]int, len(t.count)+len(ops))
	for k, c := range t.count {
		sim[k] = c
	}
	simSize := t.size
	for i, op := range ops {
		switch op.Kind {
		case OpAdd:
			if simSize >= t.maxElements {
				return fmt.Errorf("%w: operation %d (add %d): current size %d at limit %d",
					ErrLimitExceeded, i, op.V, simSize, t.maxElements)
			}
			sim[op.V]++
			simSize++
		case OpRemove:
			if sim[op.V] == 0 {
				return fmt.Errorf("%w: operation %d (remove %d): no live copy",
					ErrNotFound, i, op.V)
			}
			sim[op.V]--
			if sim[op.V] == 0 {
				delete(sim, op.V)
			}
			simSize--
		default:
			return fmt.Errorf("%w: operation %d: unknown kind %d", ErrInvalidArgument, i, op.Kind)
		}
	}

	// 全部合法：依次真正提交。
	for _, op := range ops {
		switch op.Kind {
		case OpAdd:
			t.addLocked(op.V)
		case OpRemove:
			t.removeLocked(op.V)
		}
	}
	t.rebalance()
	return t.checkLocked()
}

// addLocked 按与较小堆顶的比较决定新值入哪一侧。
func (t *Tracker) addLocked(v int) {
	t.lo.prune()
	if t.lo.valid > 0 && v > t.lo.top() {
		t.hi.push(v)
	} else {
		t.lo.push(v)
	}
	t.count[v]++
	t.size++
}

// removeLocked 登记一个有效副本作废。
// 定位规则：只要 lo 侧存在有效值且不小于 v 的最小堆顶 >= v，
// 即当前不存在严格小于 v 的有效元素，则该副本必在 lo 侧；否则在 hi 侧。
func (t *Tracker) removeLocked(v int) {
	t.lo.prune()
	t.hi.prune()
	if t.lo.valid > 0 && t.lo.top() >= v {
		t.lo.markDeleted(v)
	} else {
		t.hi.markDeleted(v)
	}
	t.count[v]--
	if t.count[v] == 0 {
		delete(t.count, v)
	}
	t.size--
	if t.size == 0 {
		t.lo.prune()
		t.hi.prune()
	}
}

// rebalance 清理堆顶作废副本并在两侧间搬运有效值，保证：
//   - lo.valid == hi.valid（偶数）或 lo.valid == hi.valid+1（奇数）；
//   - lo 每个有效元素不大于 hi 每个有效元素。
func (t *Tracker) rebalance() {
	t.lo.prune()
	t.hi.prune()
	for t.lo.valid > t.hi.valid+1 {
		t.hi.push(t.lo.popValid())
		t.lo.prune()
	}
	for t.lo.valid < t.hi.valid {
		t.lo.push(t.hi.popValid())
		t.hi.prune()
	}
	t.lo.prune()
	t.hi.prune()
	if t.lo.valid > 0 && t.hi.valid > 0 && t.lo.top() > t.hi.top() {
		loTop := t.lo.popValid()
		hiTop := t.hi.popValid()
		t.lo.push(hiTop)
		t.hi.push(loTop)
		t.lo.prune()
		t.hi.prune()
	}
}

// Op 表示一次原子批处理中的单个操作。
type Op struct {
	Kind OpKind
	V    int
}

// OpKind 标识操作类型。
type OpKind int

const (
	OpAdd    OpKind = 1
	OpRemove OpKind = 2
)
