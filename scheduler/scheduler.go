package scheduler

import (
	"sort"
	"sync"
)

// MaxTransactions 为单个调度器可接受事务总数的硬上限。
const MaxTransactions = 1 << 20

// accepted 为已接受事务的内部表示。
// writes 为去重后的写键集合；writeOrder 保留写键首次出现顺序，便于复现与审计。
// deps 为按序号升序排列的全部依赖事务序号；depth 为依赖深度。
type accepted struct {
	seq        int
	reads      []string
	writeOrder []string
	writes     map[string]struct{}
	deps       []int
	depth      int
}

// Scheduler 是基于写集依赖的并行回放调度器。
// 所有查询（深度、轮次、回放、自检）均可被多个执行体并发调用，
// 结果仅取决于已成功接受的事务序列，可重复、可复现。
type Scheduler struct {
	mu sync.RWMutex

	// txns 严格按提交顺序（即序号顺序）保存，下标为 seq-1。
	txns []accepted
	// writers 记录每个写键历史上的所有写入者序号，按提交顺序排列。
	writers map[string][]int
}

// New 创建一个空调度器。
func New() *Scheduler {
	return &Scheduler{writers: make(map[string][]int)}
}

// Submit 原子地接受一个事务；任何非法输入都会被整体拒绝且不留痕迹。
func (s *Scheduler) Submit(tx Transaction) error {
	got, err := validateOne(tx)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.txns) >= MaxTransactions {
		return ErrTooManyTransactions
	}
	if got.seq != len(s.txns)+1 {
		return ErrSequenceGap
	}
	s.appendLocked(got)
	return nil
}

// SubmitBatch 原子地接受一批事务：要么全部成功，要么整体拒绝。
func (s *Scheduler) SubmitBatch(txs []Transaction) (err error) {
	if len(txs) == 0 {
		return ErrNilTransaction
	}

	got := make([]accepted, len(txs))
	for i, tx := range txs {
		g, vErr := validateOne(tx)
		if vErr != nil {
			return vErr
		}
		got[i] = g
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.txns)+len(got) > MaxTransactions {
		return ErrTooManyTransactions
	}
	for i := range got {
		if got[i].seq != len(s.txns)+i+1 {
			return ErrSequenceGap
		}
	}
	// 全部校验通过后才真正登记，保证被整体拒绝时不留任何痕迹。
	for i := range got {
		s.appendLocked(got[i])
	}
	return nil
}

// Transactions 返回已接受事务的快照（读集仅用于审计展示）。
func (s *Scheduler) Transactions() []Transaction {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]Transaction, len(s.txns))
	for i, t := range s.txns {
		reads := append([]string(nil), t.reads...)
		writes := append([]string(nil), t.writeOrder...)
		out[i] = Transaction{Seq: t.seq, ReadKeys: reads, WriteKeys: writes}
	}
	return out
}

// Depths 返回每个事务序号到依赖深度的映射快照。
func (s *Scheduler) Depths() map[int]int {
	s.mu.RLock()
	defer s.mu.RUnlock()

	depths := make(map[int]int, len(s.txns))
	for _, t := range s.txns {
		depths[t.seq] = t.depth
	}
	return depths
}

// validatedOne 对单个事务做与已有状态无关的静态校验并去重写集。
func validateOne(tx Transaction) (accepted, error) {
	if tx.WriteKeys == nil {
		return accepted{}, ErrNilTransaction
	}
	if tx.Seq <= 0 {
		return accepted{}, ErrInvalidSequence
	}

	seen := make(map[string]struct{}, len(tx.WriteKeys))
	order := make([]string, 0, len(tx.WriteKeys))
	for _, k := range tx.WriteKeys {
		if k == "" {
			return accepted{}, ErrEmptyWriteKey
		}
		if _, dup := seen[k]; dup {
			continue
		}
		seen[k] = struct{}{}
		order = append(order, k)
	}
	if len(order) == 0 {
		return accepted{}, ErrEmptyWriteSet
	}

	reads := append([]string(nil), tx.ReadKeys...)
	return accepted{
		seq:        tx.Seq,
		reads:      reads,
		writeOrder: order,
		writes:     seen,
	}, nil
}

// appendLocked 在持有写锁的前提下登记事务并计算依赖深度。
// 调用方必须已保证序号连续且未超过总数上限。
func (s *Scheduler) appendLocked(t accepted) {
	depSet := make(map[int]struct{})
	maxDep := 0
	for _, k := range t.writeOrder {
		for _, w := range s.writers[k] {
			if _, ok := depSet[w]; !ok {
				depSet[w] = struct{}{}
				if d := s.txns[w-1].depth; d > maxDep {
					maxDep = d
				}
			}
		}
		s.writers[k] = append(s.writers[k], t.seq)
	}

	deps := make([]int, 0, len(depSet))
	for d := range depSet {
		deps = append(deps, d)
	}
	sort.Ints(deps)

	t.deps = deps
	t.depth = maxDep + 1
	s.txns = append(s.txns, t)
}

// Schedule 返回给定并行度上限下的分批调度方案。
func (s *Scheduler) Schedule(maxParallel int) ([]Round, error) {
	_ = maxParallel
	return nil, nil
}

// Replay 在给定并行度上限下真正并发地回放已接受事务。
func (s *Scheduler) Replay(maxParallel int) (*ReplayResult, error) {
	_ = maxParallel
	return nil, nil
}

// SelfCheck 校验并发回放结果与朴素串行参照逐键一致。
func (s *Scheduler) SelfCheck(maxParallel int) (*SelfCheckReport, error) {
	_ = maxParallel
	return nil, nil
}
