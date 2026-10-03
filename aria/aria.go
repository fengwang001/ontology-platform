// Package aria 实现 Aria 式确定性批处理事务执行器。
//
// 一批事务先在批起点快照上并行试执行，再用按键的最小事务号预留表
// 判定可提交者，其余按事务号顺序回退串行重执行，使每个事务的阶段、
// 读到的值与最终状态可精确复现。
package aria

import (
	"errors"
	"sync"
)

const (
	MinKeys  = 1
	MaxKeys  = 64
	MinBatch = 1
	MaxBatch = 16
	MinOps   = 1
	MaxOps   = 8
	MaxDelta = 1_000_000
)

var (
	ErrInvalidConfig = errors.New("aria: 配置非法：键数须为 1..64 且批容量须为 1..16")
	ErrOpCount       = errors.New("aria: 操作条数须为 1..8")
	ErrKeyRange      = errors.New("aria: 键越界")
	ErrDeltaRange    = errors.New("aria: 增量越界")
)

// OpKind 区分读操作与写操作。
type OpKind int

const (
	Read OpKind = iota
	Write
)

// Op 是一条事务操作：R(k) 或 W(k,d)。
type Op struct {
	Kind  OpKind
	Key   int
	Delta int64
}

// R 构造读操作 R(k)。
func R(k int) Op { return Op{Kind: Read, Key: k} }

// W 构造写操作 W(k,d)。
func W(k int, d int64) Op { return Op{Kind: Write, Key: k, Delta: d} }

// Phase 是事务在批内的结局。
type Phase int

const (
	// PhaseParallel 表示在并行阶段提交。
	PhaseParallel Phase = iota
	// PhaseFallback 表示回退后串行重执行并写入。
	PhaseFallback
	// PhaseFailed 表示溢出失败（并行试执行或回退重执行时）。
	PhaseFailed
)

func (p Phase) String() string {
	switch p {
	case PhaseParallel:
		return "并行"
	case PhaseFallback:
		return "回退"
	case PhaseFailed:
		return "失败"
	}
	return "未知"
}

// Result 是批内一个事务的执行结果；失败者无 acc（HasAcc 为假）。
type Result struct {
	TxID   uint64
	Phase  Phase
	Acc    int64
	HasAcc bool
}

type transaction struct {
	id  uint64
	ops []Op
}

// decision 记录预留表判定依据，仅供包内测试观察。
type decision struct {
	id       uint64
	waw, raw bool
	war      bool
	commit   bool
}

// Executor 是确定性批处理事务执行器，可并发调用。
type Executor struct {
	mu      sync.Mutex
	k       int
	bsz     int
	state   []int64
	nextID  uint64
	pending []transaction

	lastTouches   int
	lastDecisions []decision
}

// New 构造执行器；k 为键数（1..64，初值均为 0），bsz 为批容量（1..16）。
// 参数越界时整体拒绝。
func New(k, bsz int) (*Executor, error) {
	if k < MinKeys || k > MaxKeys || bsz < MinBatch || bsz > MaxBatch {
		return nil, ErrInvalidConfig
	}
	return &Executor{k: k, bsz: bsz, state: make([]int64, k), nextID: 1}, nil
}

// Submit 登记一个事务，返回从 1 起递增的事务号。
// 拒绝原因按顺序只报第一个：操作条数、键越界、增量越界；
// 被拒绝的调用不改变任何状态，也不消耗事务号。
func (e *Executor) Submit(ops []Op) (uint64, error) {
	if len(ops) < MinOps || len(ops) > MaxOps {
		return 0, ErrOpCount
	}
	for _, op := range ops {
		if op.Key < 0 || op.Key >= e.k {
			return 0, ErrKeyRange
		}
	}
	for _, op := range ops {
		if op.Kind == Write && (op.Delta < -MaxDelta || op.Delta > MaxDelta) {
			return 0, ErrDeltaRange
		}
	}
	cp := make([]Op, len(ops))
	copy(cp, ops)
	e.mu.Lock()
	id := e.nextID
	e.nextID++
	e.pending = append(e.pending, transaction{id: id, ops: cp})
	e.mu.Unlock()
	return id, nil
}

// Value 返回键 k 的当前值；键越界时报错且不改状态。
func (e *Executor) Value(k int) (int64, error) {
	if k < 0 || k >= e.k {
		return 0, ErrKeyRange
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.state[k], nil
}

// Pending 返回待处理事务数。
func (e *Executor) Pending() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.pending)
}
