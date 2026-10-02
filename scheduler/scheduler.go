// Package scheduler 提供声明读写集的事务批调度器。
//
// 调度器按到达顺序为事务分配连续编号（从 1 起），并在不违背冲突先后的
// 前提下尽量并行地放行事务，使执行结果等价于按到达顺序的串行执行。
package scheduler

import (
	"errors"
	"sort"
	"sync"
)

// Status 表示事务的生命周期状态。
type Status int

const (
	// Waiting 事务已到达，正在等待放行。
	Waiting Status = iota
	// Running 事务已被放行，正在执行。
	Running
	// Completed 事务已完成（成功或失败都算）。
	Completed
)

// String 返回状态的可读名称。
func (s Status) String() string {
	switch s {
	case Waiting:
		return "waiting"
	case Running:
		return "running"
	case Completed:
		return "completed"
	default:
		return "unknown"
	}
}

var (
	// ErrEmptySets 表示到达时读集与写集皆为空。
	ErrEmptySets = errors.New("scheduler: read set and write set are both empty")
	// ErrEmptyKey 表示读集或写集中含有空串键。
	ErrEmptyKey = errors.New("scheduler: key must not be empty string")
	// ErrNotFound 表示事务编号不存在（含已取消的编号）。
	ErrNotFound = errors.New("scheduler: transaction not found")
	// ErrAlreadyCompleted 表示事务已完成。
	ErrAlreadyCompleted = errors.New("scheduler: transaction already completed")
	// ErrNotRunning 表示完成操作针对的事务并非运行中。
	ErrNotRunning = errors.New("scheduler: transaction is not running")
	// ErrRunning 表示取消操作针对的事务正在运行。
	ErrRunning = errors.New("scheduler: transaction is running")
)

// txn 是调度器内部的事务记录。
type txn struct {
	id     int
	reads  map[string]struct{}
	writes map[string]struct{}
	status Status
}

// Scheduler 是声明读写集的事务批调度器，所有方法均可并发调用。
type Scheduler struct {
	mu      sync.Mutex
	k       int
	nextID  int
	txs     map[int]*txn
	running int
}

// New 创建并发上限为 k 的调度器，k 必须为正整数。
func New(k int) *Scheduler {
	if k < 1 {
		panic("scheduler: concurrency limit must be a positive integer")
	}
	return &Scheduler{k: k, nextID: 1, txs: make(map[int]*txn)}
}

// Arrive 登记一个声明了读集与写集的事务，返回其编号与本次放行的编号序列。
//
// 读集与写集皆为空时报 ErrEmptySets；含有空串键时报 ErrEmptyKey；
// 两类错误按此顺序只报第一个，且被拒绝的到达不改变任何状态。
func (s *Scheduler) Arrive(reads, writes []string) (int, []int, error) {
	if len(reads) == 0 && len(writes) == 0 {
		return 0, nil, ErrEmptySets
	}
	for _, key := range reads {
		if key == "" {
			return 0, nil, ErrEmptyKey
		}
	}
	for _, key := range writes {
		if key == "" {
			return 0, nil, ErrEmptyKey
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	id := s.nextID
	s.nextID++
	s.txs[id] = &txn{id: id, reads: toSet(reads), writes: toSet(writes), status: Waiting}
	return id, s.releaseLocked(), nil
}

// Complete 将运行中的事务标记为已完成（成功与失败都算），返回本次放行的编号序列。
//
// 编号不存在、事务已完成、事务并非运行中，按此顺序只报第一个错误并拒绝。
func (s *Scheduler) Complete(id int) ([]int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	t, ok := s.txs[id]
	if !ok {
		return nil, ErrNotFound
	}
	if t.status == Completed {
		return nil, ErrAlreadyCompleted
	}
	if t.status != Running {
		return nil, ErrNotRunning
	}
	t.status = Completed
	s.running--
	return s.releaseLocked(), nil
}

// Cancel 取消一个仍在等待的事务，取消后它不再阻挡他人，返回本次放行的编号序列。
//
// 编号不存在、事务已完成、事务正在运行，按此顺序只报第一个错误并拒绝。
func (s *Scheduler) Cancel(id int) ([]int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	t, ok := s.txs[id]
	if !ok {
		return nil, ErrNotFound
	}
	if t.status == Completed {
		return nil, ErrAlreadyCompleted
	}
	if t.status == Running {
		return nil, ErrRunning
	}
	delete(s.txs, id)
	return s.releaseLocked(), nil
}

// Blockers 返回等待中事务的阻塞者：与它冲突且编号更小、未完成的事务编号（升序）。
// 编号不存在时报 ErrNotFound；事务不在等待状态时返回空列表。
func (s *Scheduler) Blockers(id int) ([]int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	t, ok := s.txs[id]
	if !ok {
		return nil, ErrNotFound
	}
	if t.status != Waiting {
		return nil, nil
	}
	var blockers []int
	for otherID, other := range s.txs {
		if otherID >= id || other.status == Completed {
			continue
		}
		if conflict(t, other) {
			blockers = append(blockers, otherID)
		}
	}
	sort.Ints(blockers)
	return blockers, nil
}

// Status 查询事务当前状态；编号不存在（含已取消）时 ok 为 false。
func (s *Scheduler) Status(id int) (Status, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.txs[id]
	if !ok {
		return Waiting, false
	}
	return t.status, true
}

// releaseLocked 取批：按编号升序扫描等待事务，运行数已达 K 则停止；
// 否则当事务与所有编号更小且未完成的事务都不冲突时放行为运行。
// 返回本次放行的编号序列（升序）。调用方须持有 s.mu。
func (s *Scheduler) releaseLocked() []int {
	var released []int
	for id := 1; id < s.nextID; id++ {
		t, ok := s.txs[id]
		if !ok || t.status != Waiting {
			continue
		}
		if s.running >= s.k {
			break
		}
		if s.hasEarlierUnfinishedConflictLocked(t) {
			continue
		}
		t.status = Running
		s.running++
		released = append(released, id)
	}
	return released
}

// hasEarlierUnfinishedConflictLocked 报告 t 是否与某个编号更小且未完成
// （运行中或仍在等待，包括先前被跳过的）的事务冲突。调用方须持有 s.mu。
func (s *Scheduler) hasEarlierUnfinishedConflictLocked(t *txn) bool {
	for otherID, other := range s.txs {
		if otherID >= t.id || other.status == Completed {
			continue
		}
		if conflict(t, other) {
			return true
		}
	}
	return false
}

// conflict 报告两个事务是否冲突：一方的写集与另一方的读集或写集有交。
// 只读对只读不冲突。
func conflict(a, b *txn) bool {
	if intersects(a.writes, b.reads) || intersects(a.writes, b.writes) {
		return true
	}
	return intersects(b.writes, a.reads) || intersects(b.writes, a.writes)
}

func intersects(a, b map[string]struct{}) bool {
	if len(a) > len(b) {
		a, b = b, a
	}
	for key := range a {
		if _, ok := b[key]; ok {
			return true
		}
	}
	return false
}

func toSet(keys []string) map[string]struct{} {
	set := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		set[key] = struct{}{}
	}
	return set
}
