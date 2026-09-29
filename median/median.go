package median

import (
	"errors"
	"sync"
)

// OpKind 标识一批提交中的单条操作类型。
type OpKind int

const (
	// OpAdd 表示加入一个整数。
	OpAdd OpKind = iota + 1
	// OpWithdraw 表示撤回（删除）一个整数的一个副本。
	OpWithdraw
)

// Op 是一批原子提交中的单条操作。
type Op struct {
	Kind  OpKind
	Value int
}

// Tracker 维护整数多重集，动态支持加入、撤回与中位数查询。
// 零值不可直接使用，请通过 NewTracker 构造。
type Tracker struct {
	mu    sync.RWMutex
	maxN  int
	small *maxHeap    // 较小一半，堆顶为其最大值
	large *minHeap    // 较大一半，堆顶为其最小值
	count int         // 有效元素总数
	freq  map[int]int // 每个值的有效副本数（供整批校验与撤回定位）
}

// 互不可混淆的错误原因。
var (
	// ErrInvalidArgument 表示参数非法（空批次、未知操作类型等）。
	ErrInvalidArgument = errors.New("median: invalid argument")
	// ErrEmpty 表示在空多重集上查询中位数。
	ErrEmpty = errors.New("median: multiset is empty")
	// ErrNotFound 表示撤回了一个当前不存在的值。
	ErrNotFound = errors.New("median: value not present")
	// ErrLimitExceeded 表示有效元素个数将超过上限。
	ErrLimitExceeded = errors.New("median: element limit exceeded")
)

// DefaultMaxElements 是未显式指定上限时的默认容量上限。
const DefaultMaxElements = 1_000_000

// NewTracker 创建容量上限为 maxElements 的维护器；非正数使用默认上限。
func NewTracker(maxElements int) *Tracker {
	if maxElements <= 0 {
		maxElements = DefaultMaxElements
	}
	return &Tracker{
		maxN:  maxElements,
		small: newMaxHeap(),
		large: newMinHeap(),
		freq:  make(map[int]int),
	}
}

// Len 返回当前有效元素个数。
func (t *Tracker) Len() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.count
}

// Add 原子地加入一个值。
func (t *Tracker) Add(v int) error {
	return t.Commit([]Op{{Kind: OpAdd, Value: v}})
}

// Withdraw 原子地撤回一个值的一个副本；值不存在时返回 ErrNotFound。
func (t *Tracker) Withdraw(v int) error {
	return t.Commit([]Op{{Kind: OpWithdraw, Value: v}})
}

// Commit 原子地校验并提交整批操作；校验失败时状态保持不变。
func (t *Tracker) Commit(ops []Op) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if len(ops) == 0 {
		return ErrInvalidArgument
	}

	// 第一阶段：纯校验，在临时净增量账本上模拟，不触碰堆与计数。
	delta := make(map[int]int, len(ops))
	added := 0
	withdrawn := 0
	for i, op := range ops {
		switch op.Kind {
		case OpAdd:
			delta[op.Value]++
			added++
		case OpWithdraw:
			if t.freq[op.Value]+delta[op.Value] <= 0 {
				return errWithIndex(ErrNotFound, i, op)
			}
			delta[op.Value]--
			withdrawn++
		default:
			return errWithIndex(ErrInvalidArgument, i, op)
		}
	}
	if t.count+added-withdrawn < 0 {
		return ErrInvalidArgument
	}
	if t.count+added-withdrawn > t.maxN {
		return ErrLimitExceeded
	}

	// 第二阶段：全部校验通过，逐条应用并即时清理、再平衡。
	for _, op := range ops {
		switch op.Kind {
		case OpAdd:
			t.applyAdd(op.Value)
		case OpWithdraw:
			t.applyWithdraw(op.Value)
		}
	}
	t.finalize()
	t.applyFreq(delta)
	return nil
}

// Median 返回下中位数（偶数个时取偏下者）；空集返回 ErrEmpty。
func (t *Tracker) Median() (int, error) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if t.count == 0 {
		return 0, ErrEmpty
	}
	// Commit 在写锁内会保证两堆堆顶为有效元素，故只读路径可直接取较小侧堆顶。
	return t.small.top(), nil
}

// Check 执行自检，返回违反的不变量描述；无违反时返回空串与 nil。
func (t *Tracker) Check() (string, error) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	if t.count < 0 {
		return "total count is negative", nil
	}
	if t.small.valid != len(t.small.data)-staleSum(t.small.pending) {
		return "small side valid count mismatches heap ledger", nil
	}
	if t.large.valid != len(t.large.data)-staleSum(t.large.pending) {
		return "large side valid count mismatches heap ledger", nil
	}
	if t.small.valid+t.large.valid != t.count {
		return "side valid counts do not sum to total", nil
	}
	diff := t.small.valid - t.large.valid
	if diff != 0 && diff != 1 {
		return "size balance invariant violated (|small-large| > 1 or small < large)", nil
	}

	// 在副本上排空，收集有效值并验证跨侧有序性与堆结构清理后的计数。
	small := t.small.cloneForCheck()
	large := t.large.cloneForCheck()
	small.prune()
	large.prune()
	var maxSmall, minLarge int
	if small.valid > 0 {
		maxSmall = small.top()
	}
	if large.valid > 0 {
		minLarge = large.top()
	}
	if small.valid > 0 && large.valid > 0 && maxSmall > minLarge {
		return "ordering invariant violated: small element exceeds large element", nil
	}

	// 校验 freq 账本与有效计数一致。
	freqTotal := 0
	for _, c := range t.freq {
		if c < 0 {
			return "negative frequency in ledger", nil
		}
		freqTotal += c
	}
	if freqTotal != t.count {
		return "frequency ledger total mismatches count", nil
	}
	if t.count > 0 && t.freq[maxSmall] == 0 {
		return "median value missing from frequency ledger", nil
	}
	return "", nil
}

// applyAdd 按与较小侧堆顶比较决定入哪侧。调用方持写锁。
func (t *Tracker) applyAdd(v int) {
	t.small.prune()
	t.large.prune()
	if t.small.valid == 0 || v <= t.small.top() {
		t.small.push(v)
	} else {
		t.large.push(v)
	}
	t.rebalance()
}

// applyWithdraw 登记待删、扣减对应侧有效计数，再清理与再平衡。调用方持写锁。
func (t *Tracker) applyWithdraw(v int) {
	t.small.prune()
	t.large.prune()
	// 由校验保证 v 至少有一个有效副本；有效副本必在某侧堆中。
	if t.small.valid > 0 && v <= t.small.top() {
		t.small.markStale(v)
		t.small.prune()
	} else {
		t.large.markStale(v)
		t.large.prune()
	}
	t.rebalance()
}

// rebalance 保证 small.valid == large.valid 或 small.valid == large.valid+1。
func (t *Tracker) rebalance() {
	for {
		t.small.prune()
		t.large.prune()
		switch {
		case t.small.valid > t.large.valid+1:
			t.small.moveTopTo(&t.large.half)
		case t.small.valid < t.large.valid:
			t.large.moveTopTo(&t.small.half)
		default:
			return
		}
	}
}

// finalize 在一批操作应用后统一再平衡并重算总数。
func (t *Tracker) finalize() {
	t.rebalance()
	t.count = t.small.valid + t.large.valid
}

func (t *Tracker) applyFreq(delta map[int]int) {
	for v, d := range delta {
		c := t.freq[v] + d
		if c == 0 {
			delete(t.freq, v)
		} else {
			t.freq[v] = c
		}
	}
}

func staleSum(m map[int]int) int {
	s := 0
	for _, v := range m {
		s += v
	}
	return s
}
