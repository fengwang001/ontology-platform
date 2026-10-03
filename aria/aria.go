// Package aria 实现 Aria 式确定性批处理事务执行器。
//
// 一批事务先在批起点快照上并行试执行，再用按键的最小事务号预留表
// 判定可提交者，其余按事务号顺序回退串行重执行，使每个事务的阶段、
// 读到的值与最终状态可精确复现。
package aria

import (
	"errors"
	"fmt"
	"sync"
)

// 键数、批容量、操作条数与增量的合法范围。
const (
	MaxKeys  = 64
	MaxBatch = 16
	MaxOps   = 8
	MaxDelta = 1_000_000
	MinDelta = -1_000_000
)

// 配置与提交拒绝原因。
var (
	ErrInvalidConfig = errors.New("aria: 构造参数越界")
	ErrOpCount       = errors.New("aria: 操作条数不在 1 到 8")
	ErrKeyRange      = errors.New("aria: 键越界")
	ErrDeltaRange    = errors.New("aria: 增量 d 越界")
)

// OpKind 区分读操作与写操作。
type OpKind int

const (
	// OpRead 读键 k 的当前值并累加进 acc。
	OpRead OpKind = iota
	// OpWrite 把 acc+d 记入键 k 的缓冲。
	OpWrite
)

// Op 是一条事务操作：R(k) 或 W(k,d)。
type Op struct {
	Kind OpKind
	Key  int
	D    int64
}

// R 构造读操作 R(k)。
func R(k int) Op { return Op{Kind: OpRead, Key: k} }

// W 构造写操作 W(k,d)。
func W(k int, d int64) Op { return Op{Kind: OpWrite, Key: k, D: d} }

// Phase 是事务在批内的结局阶段。
type Phase int

const (
	// PhaseParallel 在并行阶段试执行成功并直接提交。
	PhaseParallel Phase = iota
	// PhaseFallback 并行阶段被中止，回退后串行重执行成功。
	PhaseFallback
	// PhaseFailed 任一步 acc 或缓冲值溢出 int64，事务失败。
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

// Result 是批内一个事务的结局。
type Result struct {
	TxID   uint64 // 事务号，从 1 起递增
	Phase  Phase  // 并行 / 回退 / 失败
	Acc    int64  // 最终 acc，仅 HasAcc 为真时有效
	HasAcc bool   // 失败者无 acc
	Reason string // 判定依据（预留表命中情况），供日志与测试核对
}

// tx 是已登记的待处理事务。
type tx struct {
	id  uint64
	ops []Op
}

// Executor 是确定性批处理事务执行器。
// 所有导出方法可并发调用，内部串行化保证结果等价于某个串行顺序。
type Executor struct {
	mu      sync.Mutex
	k       int
	bsz     int
	state   []int64
	nextID  uint64
	pending []tx
	// lastTouches 是上一批预留表建立与判定触及的不同表项数
	// （非导出计数器，供测试验证不超过批内操作总数的两倍）。
	lastTouches int
}

// New 构造执行器：K 个键（编号 0..K-1，初值均为 0），批容量 Bsz。
// K 须在 1..64，Bsz 须在 1..16，否则整体拒绝。
func New(K, Bsz int) (*Executor, error) {
	if K < 1 || K > MaxKeys || Bsz < 1 || Bsz > MaxBatch {
		return nil, fmt.Errorf("%w: K=%d Bsz=%d", ErrInvalidConfig, K, Bsz)
	}
	return &Executor{
		k:      K,
		bsz:    Bsz,
		state:  make([]int64, K),
		nextID: 1,
	}, nil
}

// Submit 登记一个事务并返回从 1 起递增的事务号。
// 拒绝原因按顺序只报第一个：操作条数、键越界、d 越界。
// 被拒绝的调用不改变任何状态，也不消耗事务号。
func (e *Executor) Submit(ops []Op) (uint64, error) {
	if len(ops) < 1 || len(ops) > MaxOps {
		return 0, fmt.Errorf("%w: %d 条", ErrOpCount, len(ops))
	}
	for _, op := range ops {
		if op.Key < 0 || op.Key >= e.k {
			return 0, fmt.Errorf("%w: 键 %d（K=%d）", ErrKeyRange, op.Key, e.k)
		}
		if op.Kind == OpWrite && (op.D < MinDelta || op.D > MaxDelta) {
			return 0, fmt.Errorf("%w: d=%d", ErrDeltaRange, op.D)
		}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	id := e.nextID
	e.nextID++
	cp := make([]Op, len(ops))
	copy(cp, ops)
	e.pending = append(e.pending, tx{id: id, ops: cp})
	return id, nil
}

// Value 返回键 k 的当前值；键越界时报错且不改状态。
func (e *Executor) Value(k int) (int64, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if k < 0 || k >= e.k {
		return 0, fmt.Errorf("%w: 键 %d（K=%d）", ErrKeyRange, k, e.k)
	}
	return e.state[k], nil
}

// Pending 返回待处理事务数。
func (e *Executor) Pending() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.pending)
}

// RunBatch 取最早的至多 Bsz 个待处理事务执行一个批次，
// 返回每个事务的结局（按事务号升序）。无待处理事务时返回空且不改状态。
func (e *Executor) RunBatch() []Result {
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.pending) == 0 {
		return nil
	}
	return e.runBatchLocked()
}
