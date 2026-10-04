// Package exec 实现排队、派发与结果分发的远程执行调度器。
package exec

import (
	"sync"

	"ontology/action"
	"ontology/worker"
)

// 重导出哨兵错误，调用方可直接使用 exec.ErrXxx 或 errors.Is。
var (
	ErrInvalid      = action.ErrInvalid
	ErrNotFound     = action.ErrNotFound
	ErrExists       = action.ErrExists
	ErrState        = action.ErrState
	ErrAttemptStale = action.ErrAttemptStale
	ErrNoFreeSlot   = action.ErrNoFreeSlot
)

// PollResult 是一次成功 Poll 的返回。
type PollResult struct {
	Op      *action.Op
	Attempt int
}

// Scheduler 合并相同摘要的请求并调度到工作者。
type Scheduler struct {
	mu       sync.Mutex
	maxLoss  int
	registry *action.Registry
	cache    *action.Cache
	pool     *worker.Pool

	queue    *priorityQueue
	waiters  map[int]*action.Waiter
	nextSeq  int
	nextWait int
}

// New 创建调度器；M 为单个操作允许的最大丢失次数（1 到 5）。
func New(M int) *Scheduler {
	if M < 1 || M > 5 {
		panic("exec: M must be in 1..5")
	}
	return &Scheduler{
		maxLoss:  M,
		registry: action.NewRegistry(),
		cache:    action.NewCache(),
		pool:     worker.NewPool(),
		queue:    newPriorityQueue(),
		waiters:  make(map[int]*action.Waiter),
	}
}

func validPlatform(p action.Platform) bool {
	if len(p) > 8 {
		return false
	}
	for _, kv := range p {
		if kv.Key == "" || kv.Value == "" {
			return false
		}
	}
	return true
}

func validProps(p []action.KV) bool { return validPlatform(p) }

// Execute 提交或附着一个操作，返回等待者编号。
func (s *Scheduler) Execute(digest string, platform action.Platform, prio int, skipCache bool) (int, error) {
	if digest == "" || !validPlatform(platform) || prio < 0 || prio > 9 {
		return 0, ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	if !skipCache {
		if exit, ok := s.registry.LookupCache(s.cache, digest); ok {
			id := s.allocWaiter(prio)
			w := action.NewWaiter(id, prio)
			s.waiters[id] = w
			w.Finish(action.Outcome{Kind: action.Cached, Exit: exit})
			return id, nil
		}
	}

	if o, ok := s.registry.LookupInFlight(digest); ok {
		if !platform.Equal(o.Platform) {
			return 0, ErrInvalid
		}
		id := s.allocWaiter(prio)
		w := action.NewWaiter(id, prio)
		s.waiters[id] = w
		o.Waiters[id] = w
		if o.State == action.Queued {
			s.queue.fix(o)
		}
		return id, nil
	}

	id := s.allocWaiter(prio)
	w := action.NewWaiter(id, prio)
	s.waiters[id] = w
	s.nextSeq++
	o := action.NewOp(digest, append(action.Platform(nil), platform...), s.nextSeq, w)
	s.registry.Add(o)
	s.queue.push(o)
	return id, nil
}

// Register 注册工作者；slots 为 1 到 64。
func (s *Scheduler) Register(name string, props []action.KV, slots int) error {
	if name == "" || !validProps(props) || slots < 1 || slots > 64 {
		return ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.pool.Add(worker.New(name, props, slots)) {
		return ErrExists
	}
	return nil
}

// Poll 派发一个匹配的排队操作。
func (s *Scheduler) Poll(name string) (PollResult, error) {
	if name == "" {
		return PollResult{}, ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	w, ok := s.pool.Get(name)
	if !ok {
		return PollResult{}, ErrNotFound
	}
	if w.Free() == 0 {
		return PollResult{}, ErrNoFreeSlot
	}

	var skipped []*action.Op
	var chosen *action.Op
	for s.queue.Len() > 0 {
		o := s.queue.pop()
		if o.Platform.SubsetOf(w.Props) {
			chosen = o
			break
		}
		skipped = append(skipped, o)
	}
	for _, o := range skipped {
		s.queue.push(o)
	}
	if chosen == nil {
		return PollResult{}, nil
	}

	chosen.State = action.Assigned
	w.Assign(chosen)
	return PollResult{Op: chosen, Attempt: chosen.Losses + 1}, nil
}

// Complete 上报操作结果；infra 为真按一次丢失处理。
func (s *Scheduler) Complete(name string, op *action.Op, attempt, exit int, infra bool) error {
	if name == "" || op == nil || attempt < 1 {
		return ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	w, ok := s.pool.Get(name)
	if !ok {
		return ErrNotFound
	}
	if cur, exists := s.registry.Get(op.Digest); !exists || cur != op || !w.Holds(op) {
		return ErrState
	}
	if attempt != op.Losses+1 {
		return ErrAttemptStale
	}

	if infra {
		w.Release(op)
		s.handleLoss(op)
		return nil
	}

	w.Release(op)
	if exit == 0 {
		s.cache.Put(op.Digest, exit)
	}
	for _, wt := range op.Waiters {
		wt.Finish(action.Outcome{Kind: action.Result, Exit: exit})
	}
	op.State = action.Done
	s.registry.Delete(op.Digest)
	return nil
}

// WorkerLost 注销工作者并对其持有的操作按 seq 升序逐个丢失处理。
func (s *Scheduler) WorkerLost(name string) error {
	if name == "" {
		return ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	w, ok := s.pool.Get(name)
	if !ok {
		return ErrNotFound
	}
	held := w.Held()
	for _, o := range held {
		w.Release(o)
		s.handleLoss(o)
	}
	s.pool.Remove(name)
	return nil
}

// Cancel 取消等待者。
func (s *Scheduler) Cancel(waiterID int) error {
	if waiterID < 1 {
		return ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	w, ok := s.waiters[waiterID]
	if !ok {
		return ErrNotFound
	}
	if w.Terminal() {
		return ErrState
	}

	var owner *action.Op
	for _, o := range s.registry.All() {
		if _, attached := o.Waiters[waiterID]; attached {
			owner = o
			break
		}
	}

	w.Finish(action.Outcome{Kind: action.Cancelled})
	if owner == nil {
		return nil
	}
	delete(owner.Waiters, waiterID)

	if len(owner.Waiters) == 0 {
		switch owner.State {
		case action.Queued:
			s.queue.remove(owner)
			owner.State = action.Done
			s.registry.Delete(owner.Digest)
		case action.Assigned:
			owner.State = action.Abandoned
		}
		return nil
	}
	if owner.State == action.Queued {
		s.queue.fix(owner)
	}
	return nil
}

// Waiter 按编号读取等待者（供接收终局）。
func (s *Scheduler) Waiter(id int) (*action.Waiter, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	w, ok := s.waiters[id]
	return w, ok
}

// QueueOrder 返回队列中操作摘要的当前次序（调试/测试用）。
func (s *Scheduler) QueueOrder() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.queue.order()
}

// Cached 返回缓存中的退出码（调试/测试用）。
func (s *Scheduler) Cached(digest string) (int, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cache.Get(digest)
}

func (s *Scheduler) allocWaiter(prio int) int {
	s.nextWait++
	return s.nextWait
}

// handleLoss 调用方必须持锁，且操作刚从工作者槽位释放。
func (s *Scheduler) handleLoss(o *action.Op) {
	o.Losses++
	if len(o.Waiters) == 0 {
		s.queue.remove(o)
		o.State = action.Done
		s.registry.Delete(o.Digest)
		return
	}
	if o.Losses >= s.maxLoss {
		for _, wt := range o.Waiters {
			wt.Finish(action.Outcome{Kind: action.Lost})
		}
		o.State = action.Done
		s.registry.Delete(o.Digest)
		return
	}
	// 有等待者的只可能是 Queued/Assigned；Abandoned（无等待者）
	// 已在上方 len==0 分支直接删除，不回队也不产生终局。
	s.queue.remove(o)
	s.queue.push(o)
}
