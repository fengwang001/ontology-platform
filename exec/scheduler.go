package exec

import (
	"ontology/action"
	"ontology/worker"
	"sort"
	"sync"
)

// Scheduler 是合并相同动作的远程执行调度器。
type Scheduler struct {
	mu         sync.Mutex
	m          int
	idx        *action.Index
	workers    map[string]*worker.Worker
	waiters    map[int]*action.Waiter
	queue      []*action.Op
	nextWaiter int
	nextSeq    int
}

func New(M int) *Scheduler {
	if M < 1 || M > 5 {
		panic("exec: M must be in 1..5")
	}
	return &Scheduler{
		m:       M,
		idx:     action.NewIndex(),
		workers: map[string]*worker.Worker{},
		waiters: map[int]*action.Waiter{},
	}
}

func validPlatform(p action.Platform) bool { return len(p) <= 8 }

// Execute 返回等待者编号（被拒不占号）。skipCache 用 bool。
func (s *Scheduler) Execute(digest string, platform action.Platform, prio int, skipCache bool) (int, error) {
	if digest == "" || !validPlatform(platform) || prio < 0 || prio > 9 {
		return 0, ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	op, cachedExit, cached := s.idx.Lookup(digest, skipCache)
	if cached {
		id := s.allocWaiter()
		w := s.newWaiter(id, prio)
		s.waiters[id] = w
		w.DeliverOne(action.Outcome{Kind: "Cached", Exit: cachedExit, Cached: true})
		return id, nil
	}
	if op != nil {
		if !action.EqualPlatform(op.Platform, platform) {
			return 0, ErrInvalid
		}
		id := s.allocWaiter()
		w := s.newWaiter(id, prio)
		s.waiters[id] = w
		oldPrio := op.Prio
		op.AddWaiter(w)
		if op.Status == action.Queued && op.Prio != oldPrio {
			s.sortQueue()
		}
		return id, nil
	}

	id := s.allocWaiter()
	w := s.newWaiter(id, prio)
	s.waiters[id] = w
	s.nextSeq++
	op = &action.Op{
		Digest:   digest,
		Platform: clonePlatform(platform),
		Seq:      s.nextSeq,
		Status:   action.Queued,
		Waiters:  map[int]*action.Waiter{},
	}
	op.AddWaiter(w)
	s.idx.PutInflight(op)
	s.queue = append(s.queue, op)
	s.sortQueue()
	return id, nil
}

func (s *Scheduler) Register(name string, props map[string]string, slots int) error {
	if slots < 1 || slots > 64 {
		return ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.workers[name]; ok {
		return ErrExists
	}
	s.workers[name] = worker.New(name, props, slots)
	return nil
}

func (s *Scheduler) Poll(name string) (digest string, attempt int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	w, ok := s.workers[name]
	if !ok {
		return "", 0, ErrNotFound
	}
	if w.FreeSlots() == 0 {
		return "", 0, ErrNoSlot
	}
	idx := -1
	for i, op := range s.queue {
		if action.MatchedBy(op.Platform, w.Props) {
			idx = i
			break
		}
	}
	if idx < 0 {
		return "", 0, nil
	}
	op := s.queue[idx]
	s.queue = append(s.queue[:idx], s.queue[idx+1:]...)
	op.Status = action.Assigned
	op.Holder = name
	op.Attempt = op.Losses + 1
	w.Acquire(op.Digest)
	return op.Digest, op.Attempt, nil
}

func (s *Scheduler) Complete(name, digest string, attempt, exit int, infra bool) error {
	if name == "" || digest == "" {
		return ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	w, ok := s.workers[name]
	if !ok {
		return ErrNotFound
	}
	op, found := s.idx.Inflight(digest)
	if !found || op.Status != action.Assigned || op.Holder != name || !w.Holds(digest) {
		return ErrState
	}
	if attempt != op.Attempt {
		return ErrStaleAttempt
	}

	w.Release(digest)
	if infra {
		s.handleLoss(op)
		return nil
	}

	op.Deliver(action.Outcome{Kind: "Result", Exit: exit})
	if exit == 0 {
		s.idx.PutCache(digest, exit)
	}
	s.finish(op)
	return nil
}

func (s *Scheduler) WorkerLost(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	w, ok := s.workers[name]
	if !ok {
		return ErrNotFound
	}
	digests := w.HeldDigests()
	ops := make([]*action.Op, 0, len(digests))
	for _, d := range digests {
		if op, found := s.idx.Inflight(d); found && op.Status == action.Assigned && op.Holder == name {
			ops = append(ops, op)
		}
	}
	sort.Slice(ops, func(i, j int) bool { return ops[i].Seq < ops[j].Seq })
	delete(s.workers, name)
	for _, op := range ops {
		s.handleLoss(op)
	}
	return nil
}

func (s *Scheduler) Cancel(waiterID int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	w, ok := s.waiters[waiterID]
	if !ok {
		return ErrNotFound
	}
	if !w.DeliverOne(action.Outcome{Kind: "Cancelled"}) {
		return ErrState
	}

	op := w.Op
	if op == nil {
		return nil
	}
	if _, still := op.Waiters[waiterID]; !still {
		return nil
	}
	delete(op.Waiters, waiterID)
	op.RecomputePrio()

	if len(op.Waiters) == 0 {
		switch op.Status {
		case action.Queued:
			s.removeFromQueue(op)
			s.finish(op)
		case action.Assigned:
			// 弃置操作：继续执行、继续占槽，不改变 Attempt/Holder。
		}
	} else if op.Status == action.Queued {
		s.sortQueue()
	}
	return nil
}

// handleLoss 处理一次丢失（infra 完成或工作者失联），调用前槽位已释放或工作者已注销。
func (s *Scheduler) handleLoss(op *action.Op) {
	op.Losses++
	op.Holder = ""
	op.Attempt = 0
	if len(op.Waiters) == 0 {
		s.finish(op)
		return
	}
	if op.Losses >= s.m {
		op.Deliver(action.Outcome{Kind: "Lost"})
		s.finish(op)
		return
	}
	op.Status = action.Queued
	s.queue = append(s.queue, op)
	s.sortQueue()
}

// finish 从在途表移除；队列移除由调用方按状态决定后执行。
func (s *Scheduler) finish(op *action.Op) {
	if op.Status == action.Queued {
		s.removeFromQueue(op)
	}
	s.idx.DeleteInflight(op.Digest)
}

func (s *Scheduler) removeFromQueue(op *action.Op) {
	for i, q := range s.queue {
		if q == op {
			s.queue = append(s.queue[:i], s.queue[i+1:]...)
			return
		}
	}
}

func (s *Scheduler) sortQueue() {
	sort.SliceStable(s.queue, func(i, j int) bool {
		if s.queue[i].Prio != s.queue[j].Prio {
			return s.queue[i].Prio > s.queue[j].Prio
		}
		return s.queue[i].Seq < s.queue[j].Seq
	})
}

func (s *Scheduler) allocWaiter() int {
	s.nextWaiter++
	return s.nextWaiter
}

func (s *Scheduler) newWaiter(id, prio int) *action.Waiter {
	return &action.Waiter{ID: id, Prio: prio, Done: make(chan action.Outcome, 1)}
}

func clonePlatform(p action.Platform) action.Platform {
	c := make(action.Platform, len(p))
	for k, v := range p {
		c[k] = v
	}
	return c
}

func (s *Scheduler) Lookups() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.idx.Lookups()
}

// WaiterOutcome 非阻塞读取等待者终局；尚未终局时 ok 为 false。
func (s *Scheduler) WaiterOutcome(id int) (action.Outcome, bool) {
	s.mu.Lock()
	w, ok := s.waiters[id]
	s.mu.Unlock()
	if !ok {
		return action.Outcome{}, false
	}
	select {
	case o := <-w.Done:
		return o, true
	default:
		return action.Outcome{}, false
	}
}

// WaiterTerminal 非破坏性地返回等待者终局快照，可重复调用。
func (s *Scheduler) WaiterTerminal(id int) (action.Outcome, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	w, ok := s.waiters[id]
	if !ok {
		return action.Outcome{}, false
	}
	return w.Snapshot()
}

func (s *Scheduler) QueueOrder() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.queue))
	for i, op := range s.queue {
		out[i] = op.Digest
	}
	return out
}

// UsedSlots 返回各工作者已占槽数之和。
func (s *Scheduler) UsedSlots() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	total := 0
	for _, w := range s.workers {
		total += w.Used()
	}
	return total
}

// AssignedCount 返回 Assigned 操作数（含弃置操作）。
func (s *Scheduler) AssignedCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.assignedCountLocked()
}

// InflightCount 返回在途操作数（Queued + Assigned，含弃置）。
func (s *Scheduler) InflightCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.queue) + s.assignedCountLocked()
}

func (s *Scheduler) assignedCountLocked() int {
	total := 0
	for _, w := range s.workers {
		total += w.Used()
	}
	return total
}
