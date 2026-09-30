package scheduler

import (
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"sort"
	"strings"
	"sync"
)

// 事务状态。
const (
	// StatusWaiting 事务已到达，因冲突或并发上限尚未放行。
	StatusWaiting = "waiting"
	// StatusRunning 事务已放行，尚未完成/取消。
	StatusRunning = "running"
	// StatusCompleted 事务已完成（成功或失败）或已被取消，此后不再阻挡他人。
	StatusCompleted = "completed"
)

// 拒绝操作时按规定顺序只报的第一个错误。
var (
	ErrEmptyReadWriteSet = errors.New("scheduler: read set and write set are both empty") // 到达：读写集皆空
	ErrEmptyKey          = errors.New("scheduler: key must not be empty string")          // 到达：含空串键
	ErrIDNotFound        = errors.New("scheduler: transaction id not found")              // 编号不存在
	ErrAlreadyCompleted  = errors.New("scheduler: transaction already completed")         // 编号已完成（含已取消）
	ErrNotRunning        = errors.New("scheduler: transaction is not running")            // 完成：并非运行中（即仍在等待）
	ErrRunning           = errors.New("scheduler: transaction is running")                // 取消：事务正在运行
)

type txn struct {
	id     int
	reads  map[string]struct{}
	writes map[string]struct{}
	status string
}

// Scheduler 按到达顺序接收事务，并在不越过任何编号更小且未完成的冲突事务的前提下，
// 在每次到达/完成/取消后立即取批，尽量并行地放行业务。
//
// 所有方法可被并发调用；内部以互斥锁串行化状态变更，因此相同操作序列重放结果完全相同。
type Scheduler struct {
	mu      sync.Mutex
	k       int
	nextID  int
	txns    map[int]*txn
	waiting []int // 等待队列，按编号升序维护
	running int
	logger  *log.Logger
}

// Option 在创建时定制调度器。
type Option func(*Scheduler)

// WithLogger 将判定日志（输入、输出、放行/阻塞依据）写到 w；默认写标准错误。
// 传 nil 可关闭日志。
func WithLogger(w io.Writer) Option {
	return func(s *Scheduler) {
		if w == nil {
			s.logger = log.New(io.Discard, "", 0)
			return
		}
		s.logger = log.New(w, "[scheduler] ", log.LstdFlags|log.Lmicroseconds)
	}
}

// New 创建并发上限为 K（必须为正整数）的事务批调度器。
func New(k int, opts ...Option) *Scheduler {
	if k <= 0 {
		panic("scheduler: concurrency limit K must be a positive integer")
	}
	s := &Scheduler{
		k:      k,
		txns:   make(map[int]*txn),
		logger: log.New(os.Stderr, "[scheduler] ", log.LstdFlags|log.Lmicroseconds),
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Arrive 登记一个新事务：readSet 与 writeSet 为字符串键集合（可有交集，重复元素去重）。
// 编号从 1 起按到达顺序连续分配；登记后立即取批。
//
// 校验按顺序只报第一个错误：读写集皆空 → 含空串键。被拒绝时不改变任何状态。
func (s *Scheduler) Arrive(readSet, writeSet []string) (id int, released []int, err error) {
	if len(readSet) == 0 && len(writeSet) == 0 {
		s.logf("arrive rejected: readSet=%v writeSet=%v reason=%q", readSet, writeSet, ErrEmptyReadWriteSet)
		return 0, nil, ErrEmptyReadWriteSet
	}
	reads, errReads := toKeySet(readSet)
	writes, errWrites := toKeySet(writeSet)
	if errReads != nil || errWrites != nil {
		s.logf("arrive rejected: readSet=%v writeSet=%v reason=%q", readSet, writeSet, ErrEmptyKey)
		return 0, nil, ErrEmptyKey
	}

	s.mu.Lock()
	s.nextID++
	t := &txn{
		id:     s.nextID,
		reads:  reads,
		writes: writes,
		status: StatusWaiting,
	}
	s.txns[t.id] = t
	s.waiting = append(s.waiting, t.id)
	s.logf("arrive id=%d readSet=%s writeSet=%s running=%d/%d -> take batch",
		t.id, keys(reads), keys(writes), s.running, s.k)
	released = s.admitLocked("arrive")
	s.mu.Unlock()
	s.logf("arrive id=%d released=%v", t.id, released)
	return t.id, released, nil
}

// Complete 将事务标记为已完成：失败与成功都算。完成后立即取批。
//
// 校验按顺序只报第一个错误：编号不存在 → 已完成 → 并非运行中。被拒绝时不改变任何状态。
func (s *Scheduler) Complete(id int) (released []int, err error) {
	s.mu.Lock()
	t, ok := s.txns[id]
	switch {
	case !ok:
		s.mu.Unlock()
		s.logf("complete rejected: id=%d reason=%q", id, ErrIDNotFound)
		return nil, ErrIDNotFound
	case t.status == StatusCompleted:
		s.mu.Unlock()
		s.logf("complete rejected: id=%d reason=%q", id, ErrAlreadyCompleted)
		return nil, ErrAlreadyCompleted
	case t.status != StatusRunning:
		s.mu.Unlock()
		s.logf("complete rejected: id=%d reason=%q", id, ErrNotRunning)
		return nil, ErrNotRunning
	}
	t.status = StatusCompleted
	s.running--
	s.logf("complete id=%d running=%d/%d -> take batch", id, s.running, s.k)
	released = s.admitLocked("complete")
	s.mu.Unlock()
	s.logf("complete id=%d released=%v", id, released)
	return released, nil
}

// Cancel 取消等待中的事务；取消后它转为已完成、不再阻挡他人，并立即取批。
//
// 校验按顺序只报第一个错误：编号不存在 → 已完成 → 正在运行。被拒绝时不改变任何状态。
func (s *Scheduler) Cancel(id int) (released []int, err error) {
	s.mu.Lock()
	t, ok := s.txns[id]
	switch {
	case !ok:
		s.mu.Unlock()
		s.logf("cancel rejected: id=%d reason=%q", id, ErrIDNotFound)
		return nil, ErrIDNotFound
	case t.status == StatusCompleted:
		s.mu.Unlock()
		s.logf("cancel rejected: id=%d reason=%q", id, ErrAlreadyCompleted)
		return nil, ErrAlreadyCompleted
	case t.status == StatusRunning:
		s.mu.Unlock()
		s.logf("cancel rejected: id=%d reason=%q", id, ErrRunning)
		return nil, ErrRunning
	}
	t.status = StatusCompleted
	kept := s.waiting[:0]
	for _, wid := range s.waiting {
		if wid != id {
			kept = append(kept, wid)
		}
	}
	s.waiting = kept
	s.logf("cancel id=%d removed from waiting -> take batch", id)
	released = s.admitLocked("cancel")
	s.mu.Unlock()
	s.logf("cancel id=%d released=%v", id, released)
	return released, nil
}

// Blockers 查询等待事务的阻塞者：与它冲突、编号更小且未完成（运行中或等待中）的事务编号，升序返回。
// 对运行中或已完成（含已取消、不存在）的事务调用不属于合法查询，分别返回
// ErrRunning 或 ErrAlreadyCompleted / ErrIDNotFound。
func (s *Scheduler) Blockers(id int) (blockers []int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.txns[id]
	switch {
	case !ok:
		return nil, ErrIDNotFound
	case t.status == StatusCompleted:
		return nil, ErrAlreadyCompleted
	case t.status == StatusRunning:
		return nil, ErrRunning
	}
	for _, otherID := range s.blockersLocked(t) {
		blockers = append(blockers, otherID)
	}
	s.logf("blockers id=%d blockers=%v", id, blockers)
	return blockers, nil
}

// Status 返回事务当前状态；不存在返回空串与 false。
func (s *Scheduler) Status(id int) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.txns[id]
	if !ok {
		return "", false
	}
	return t.status, true
}

// admitLocked 为取批过程：调用方必须持有 s.mu。
//
// 按编号升序扫描等待队列。运行数已达 K 立即停止扫描（后续事务即使不冲突也不放行，
// 以免越过被上限挡下的更早事务）；否则，仅当候选与所有编号更小且未完成的事务
// （运行中的，以及仍在等待的、包括先前被跳过的）都不冲突时放行。
// 一轮扫描结束即完成本次取批：先前被跳过的事务若因本轮后续放行而失去阻塞者，
// 留待下一次到达/完成/取消触发的取批处理——新放行的事务与之不冲突，不影响正确性。
func (s *Scheduler) admitLocked(trigger string) []int {
	released := make([]int, 0)
	remaining := s.waiting[:0]
	stopped := false
	for _, id := range s.waiting {
		if stopped {
			remaining = append(remaining, id)
			continue
		}
		t := s.txns[id]
		if s.running >= s.k {
			s.logf("trigger=%s stop scan before id=%d reason=%q running=%d/%d",
				trigger, id, "concurrency limit reached", s.running, s.k)
			remaining = append(remaining, id)
			stopped = true
			continue
		}
		blockers := s.blockersLocked(t)
		if len(blockers) > 0 {
			s.logf("trigger=%s hold id=%d blockers=%v", trigger, id, blockers)
			remaining = append(remaining, id)
			continue
		}
		t.status = StatusRunning
		s.running++
		released = append(released, id)
		s.logf("trigger=%s release id=%d running=%d/%d", trigger, id, s.running, s.k)
	}
	s.waiting = remaining
	return released
}

// blockersLocked 返回与 t 冲突、编号更小且未完成的事务编号升序。调用方持锁。
func (s *Scheduler) blockersLocked(t *txn) []int {
	blockers := make([]int, 0)
	for otherID := 1; otherID < t.id; otherID++ {
		other, ok := s.txns[otherID]
		if !ok || other.status == StatusCompleted {
			continue
		}
		if conflict(t, other) {
			blockers = append(blockers, otherID)
		}
	}
	return blockers
}

// conflict 判定两个事务是否冲突：任一方写集与另一方读集或写集有交。
// 只读事务之间永不冲突。
func conflict(a, b *txn) bool {
	return intersects(a.writes, b.reads) ||
		intersects(a.writes, b.writes) ||
		intersects(a.reads, b.writes)
}

func intersects(a, b map[string]struct{}) bool {
	if len(a) > len(b) {
		a, b = b, a
	}
	for k := range a {
		if _, ok := b[k]; ok {
			return true
		}
	}
	return false
}

func toKeySet(keys []string) (map[string]struct{}, error) {
	set := make(map[string]struct{}, len(keys))
	for _, k := range keys {
		if k == "" {
			return nil, ErrEmptyKey
		}
		set[k] = struct{}{}
	}
	return set, nil
}

func keys(set map[string]struct{}) string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return "{" + strings.Join(out, ",") + "}"
}

func (s *Scheduler) logf(format string, args ...any) {
	if s.logger != nil {
		s.logger.Output(2, fmt.Sprintf(format, args...))
	}
}
