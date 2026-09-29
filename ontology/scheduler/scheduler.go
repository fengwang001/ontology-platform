package scheduler

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

// txn 是已接受事务的规范化内部表示。写集经过去重并按键排序，
// 依赖与深度在接受时一次性计算完毕（仅依赖此前已接受的事务）。
type txn struct {
	seq       int
	readKeys  []string
	writeKeys []string
	values    map[string]string
	dependsOn []int
	depth     int
}

// Scheduler 接收按提交顺序到达的事务，计算依赖深度、分批调度轮次并支持回放。
// 所有方法均可被多个 goroutine 并发调用；Add 之间、Add 与查询之间通过
// 同一把 RWMutex 串行化，任何被拒绝的 Add 在持锁阶段完成全部校验，
// 因而拒绝不会在状态上留下任何痕迹。
type Scheduler struct {
	mu         sync.RWMutex
	txns       []*txn
	lastWriter map[string]int
}

// New 创建一个空的调度器。
func New() *Scheduler {
	return &Scheduler{lastWriter: make(map[string]int)}
}

// validate 在不触碰调度器状态的前提下校验单个事务输入。
// 错误类别互不相同：非法参数、序号不连续、写集为空、写集含空键、总数超限。
func (s *Scheduler) validate(tx Transaction) error {
	if tx.Seq <= 0 {
		return fmt.Errorf("%w: seq must be positive, got %d", ErrInvalidArgument, tx.Seq)
	}
	if len(s.txns) >= MaxTransactions {
		return fmt.Errorf("%w: %d existing transactions, limit is %d",
			ErrTxLimitExceeded, len(s.txns), MaxTransactions)
	}
	if want := len(s.txns) + 1; tx.Seq != want {
		return fmt.Errorf("%w: expected seq %d but got %d", ErrSeqNotConsecutive, want, tx.Seq)
	}
	for _, k := range tx.WriteKeys {
		if k == "" {
			return fmt.Errorf("%w: tx %d write key at index", ErrWriteSetEmptyKey, tx.Seq)
		}
	}
	if len(tx.WriteKeys) == 0 {
		return fmt.Errorf("%w: tx %d", ErrWriteSetEmpty, tx.Seq)
	}
	for _, k := range tx.ReadKeys {
		if k == "" {
			return fmt.Errorf("%w: tx %d read key at index is empty", ErrInvalidArgument, tx.Seq)
		}
	}
	if len(tx.WriteValues) > 0 {
		writeSet := make(map[string]struct{}, len(tx.WriteKeys))
		for _, k := range tx.WriteKeys {
			writeSet[k] = struct{}{}
		}
		for k := range tx.WriteValues {
			if k == "" {
				return fmt.Errorf("%w: tx %d write value mapped to empty key",
					ErrInvalidArgument, tx.Seq)
			}
			if _, ok := writeSet[k]; !ok {
				return fmt.Errorf("%w: tx %d write value for non-write key %q",
					ErrInvalidArgument, tx.Seq, k)
			}
		}
	}
	return nil
}

// normalize 去重写集并复制读集/值映射，保证内部状态不持有调用方切片。
func normalize(tx Transaction) *txn {
	seen := make(map[string]struct{}, len(tx.WriteKeys))
	keys := make([]string, 0, len(tx.WriteKeys))
	for _, k := range tx.WriteKeys {
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		keys = append(keys, k)
	}
	sort.Strings(keys)

	reads := append([]string(nil), tx.ReadKeys...)
	sort.Strings(reads)

	var values map[string]string
	if len(tx.WriteValues) > 0 {
		values = make(map[string]string, len(tx.WriteValues))
		for k, v := range tx.WriteValues {
			values[k] = v
		}
	}
	return &txn{
		seq:       tx.Seq,
		readKeys:  reads,
		writeKeys: keys,
		values:    values,
	}
}

// Add 接受一个事务；非法输入会被整体拒绝且不改变任何已有状态。
func (s *Scheduler) Add(tx Transaction) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.validate(tx); err != nil {
		return err
	}
	cur := normalize(tx)

	// 依赖只由写集相交判定：对每个写键，依赖该键的最近一个写入者。
	maxDepth := 0
	depSet := make(map[int]struct{})
	for _, k := range cur.writeKeys {
		if seq, ok := s.lastWriter[k]; ok {
			depSet[seq] = struct{}{}
		}
	}
	for seq := range depSet {
		cur.dependsOn = append(cur.dependsOn, seq)
	}
	sort.Ints(cur.dependsOn)
	for _, seq := range cur.dependsOn {
		if d := s.txns[seq-1].depth; d > maxDepth {
			maxDepth = d
		}
	}
	cur.depth = maxDepth + 1

	s.txns = append(s.txns, cur)
	for _, k := range cur.writeKeys {
		s.lastWriter[k] = cur.seq
	}
	return nil
}

// Len 返回已接受的事务数量。
func (s *Scheduler) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.txns)
}

// snapshot 在读锁下复制全部事务，供无锁的重计算/回放使用。
func (s *Scheduler) snapshot() []*txn {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*txn, len(s.txns))
	copy(out, s.txns)
	return out
}

// Depth 返回指定事务的依赖深度：无依赖为 1，否则为所有依赖深度最大值加一。
func (s *Scheduler) Depth(seq int) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if seq < 1 || seq > len(s.txns) {
		return 0, fmt.Errorf("%w: seq %d", ErrTxNotFound, seq)
	}
	return s.txns[seq-1].depth, nil
}

// reason 生成深度判定的人类可读依据。
func (t *txn) reason() string {
	if len(t.dependsOn) == 0 {
		return "write set disjoint from all prior write sets; no dependency; depth=1"
	}
	deps := make([]string, len(t.dependsOn))
	for i, seq := range t.dependsOn {
		deps[i] = fmt.Sprintf("tx%d", seq)
	}
	return fmt.Sprintf("write set intersects %s; max dependency depth=%d; depth=%d",
		strings.Join(deps, ","), t.depth-1, t.depth)
}

// Plans 返回所有已接受事务的深度与轮次计划（按序号升序）。
// 轮次按 maxParallel 计算；maxParallel <= 0 时返回 ErrInvalidParallel。
func (s *Scheduler) Plans(maxParallel int) ([]TransactionPlan, error) {
	if maxParallel <= 0 {
		return nil, fmt.Errorf("%w: got %d", ErrInvalidParallel, maxParallel)
	}
	txns := s.snapshot()
	rounds := computeRounds(txns, maxParallel)

	plans := make([]TransactionPlan, len(txns))
	for i, t := range txns {
		plans[i] = TransactionPlan{
			Seq:       t.seq,
			Depth:     t.depth,
			Round:     rounds[t.seq],
			DependsOn: append([]int(nil), t.dependsOn...),
			Reason:    t.reason(),
		}
	}
	return plans, nil
}

// computeRounds 按"序号从小到大逐轮贪心挑选"规则计算每个事务所在轮次。
// 一轮中：按序号扫描，挑出依赖均已在更早轮次完成、且本轮容量未满的事务，
// 每轮至多 maxParallel 个；同轮事务写集必然两两不相交。
func computeRounds(txns []*txn, maxParallel int) map[int]int {
	roundOf := make(map[int]int, len(txns))
	done := make(map[int]bool, len(txns))
	for round := 1; len(done) < len(txns); round++ {
		count := 0
		for _, t := range txns {
			if done[t.seq] || count >= maxParallel {
				continue
			}
			ready := true
			for _, dep := range t.dependsOn {
				if roundOf[dep] >= round {
					ready = false
					break
				}
			}
			if !ready {
				continue
			}
			roundOf[t.seq] = round
			done[t.seq] = true
			count++
		}
	}
	return roundOf
}

// Schedule 在给定并行度上限下计算逐轮调度计划。
func (s *Scheduler) Schedule(maxParallel int) ([]RoundPlan, error) {
	if maxParallel <= 0 {
		return nil, fmt.Errorf("%w: got %d", ErrInvalidParallel, maxParallel)
	}
	txns := s.snapshot()
	roundOf := computeRounds(txns, maxParallel)

	maxRound := 0
	for _, r := range roundOf {
		if r > maxRound {
			maxRound = r
		}
	}
	rounds := make([]RoundPlan, maxRound)
	for r := 1; r <= maxRound; r++ {
		rp := RoundPlan{Round: r}
		for _, t := range txns {
			if roundOf[t.seq] == r {
				rp.TxSeqs = append(rp.TxSeqs, t.seq)
			}
		}
		rp.Parallel = len(rp.TxSeqs) > 1
		rounds[r-1] = rp
	}
	return rounds, nil
}
