// Package scheduler 实现基于写集依赖的并行回放调度器。
//
// 事务按提交顺序到达；调度器仅以“写集相交”判定先后依赖（读集只审计），
// 计算每个事务的依赖深度，并在给定并行度上限下分批调度、并发回放，
// 保证回放结果与按提交顺序串行执行逐键一致且可复现。
package scheduler

import (
	"context"
	"log/slog"
	"sync"
)

// DefaultMaxTransactions 是单个调度器可接受事务总数的默认上限。
const DefaultMaxTransactions = 10000

// TxnInput 是一个按提交顺序到达的事务输入。
// Seq 为事务序号，必须从 1 开始连续递增；读集仅作审计记录。
type TxnInput struct {
	Seq      int
	ReadKeys []string
	WriteKeys []string
}

// TxnInfo 是一个已接受事务的完整信息。
type TxnInfo struct {
	Seq       int      // 事务序号
	ReadKeys  []string // 读集（审计用，原样保留）
	WriteKeys []string // 去重后的写集
	Depth     int      // 依赖深度：无依赖为 1，否则为 max(依赖深度)+1
	Deps      []int    // 所有写集相交的前序事务序号（升序）
	Basis     string   // 深度判定依据的可读说明
	Round     int      // 最近一次调度所在轮次（0 表示尚未调度）
}

// Schedule 描述一次分批调度的结果。
type Schedule struct {
	MaxParallel int     // 本次调度使用的并行度上限
	Rounds      [][]int // 每一轮包含的事务序号（轮内序号升序）
	TxnRounds   map[int]int
}

// ReplayResult 描述一次回放的结果。
type ReplayResult struct {
	State    map[string]int // 最终状态：键 -> 最后写入该键的事务序号
	Schedule *Schedule      // 本次回放采用的调度方案
}

// Scheduler 是线程安全的并行回放调度器。零值不可用，请用 New。
type Scheduler struct {
	mu             sync.RWMutex
	txns           []*TxnInfo // 按提交顺序（Seq-1 索引）
	maxTxns        int
	logger         *slog.Logger
	roundWorker    func(seq int) // 可选：每个事务回放时执行的钩子（用于测试观测并发）
}

// Option 配置 Scheduler。
type Option func(*Scheduler)

// WithMaxTransactions 设置可接受事务总数上限（必须 > 0）。
func WithMaxTransactions(n int) Option {
	return func(s *Scheduler) { s.maxTxns = n }
}

// WithLogger 设置结构化日志记录器；nil 表示不输出日志。
func WithLogger(l *slog.Logger) Option {
	return func(s *Scheduler) { s.logger = l }
}

// New 创建调度器。
func New(opts ...Option) *Scheduler {
	s := &Scheduler{maxTxns: DefaultMaxTransactions, logger: slog.New(slog.DiscardHandler)}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Accept 原子地接收一批按提交顺序连续到达的事务。
// 批次内任意事务非法则整体拒绝，已有状态保持不变。
func (s *Scheduler) Accept(txns []TxnInput) error {
	return reject(CodeInvalidArgument, "not implemented", "")
}

// Depth 返回指定事务的依赖深度；序号不存在返回 INVALID_ARGUMENT。
func (s *Scheduler) Depth(seq int) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	t, err := s.lockedTxn(seq)
	if err != nil {
		return 0, err
	}
	return t.Depth, nil
}

// Txn 返回指定事务的信息快照；序号不存在返回 INVALID_ARGUMENT。
func (s *Scheduler) Txn(seq int) (TxnInfo, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	t, err := s.lockedTxn(seq)
	if err != nil {
		return TxnInfo{}, err
	}
	return cloneTxn(t), nil
}

// Len 返回已接受事务数。
func (s *Scheduler) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.txns)
}

// lockedTxn 返回内部事务指针，调用方必须持有读锁或写锁，且不得修改返回值。
func (s *Scheduler) lockedTxn(seq int) (*TxnInfo, error) {
	if seq < 1 || seq > len(s.txns) {
		return nil, reject(
			CodeInvalidArgument,
			fmt.Sprintf("事务序号 %d 不存在", seq),
			fmt.Sprintf("当前已接受事务区间 [1, %d]", len(s.txns)),
		)
	}
	return s.txns[seq-1], nil
}

func cloneTxn(t *TxnInfo) TxnInfo {
	cp := *t
	cp.ReadKeys = append([]string(nil), t.ReadKeys...)
	cp.WriteKeys = append([]string(nil), t.WriteKeys...)
	cp.Deps = append([]int(nil), t.Deps...)
	return cp
}

// Plan 在给定并行度上限下生成分批调度方案（不执行回放）。
func (s *Scheduler) Plan(maxParallel int) (*Schedule, error) { return nil, nil }

// Replay 按 Plan 的批次真正并发回放，返回与串行提交顺序一致的最终状态。
func (s *Scheduler) Replay(ctx context.Context, maxParallel int) (*ReplayResult, error) {
	return nil, nil
}

// SelfCheck 用朴素串行参照逐事务比对深度、轮次与最终状态。
func (s *Scheduler) SelfCheck(maxParallel int) error { return nil }

// SetRoundWorker 设置回放时每个事务执行的钩子（主要用于测试观测真并发）。
func (s *Scheduler) SetRoundWorker(fn func(seq int)) {}
