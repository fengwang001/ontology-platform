// Package sched 实现多租户加权公平调度（虚拟时间最小者优先）。
package sched

import (
	"errors"
	"sync"

	"ontology/admit"
	"ontology/stat"
	"ontology/task"
	"ontology/tenant"
)

var (
	// ErrClosed 在调度器关闭后提交。
	ErrClosed = errors.New("sched: closed")
)

// Scheduler 是并发安全的加权公平队列调度器。
type Scheduler struct {
	mu      sync.Mutex
	cond    sync.Cond
	tenants map[string]*tenant.Tenant
	heap    heap
	adm     *admit.Manager
	stats   *stat.Tracker
	seq     *task.Source
	closed  bool
}

// New 创建调度器；clock 可为 nil（注入时钟仅用于统计）。
func New(clock stat.Clock) *Scheduler {
	s := &Scheduler{
		tenants: map[string]*tenant.Tenant{},
		adm:     admit.New(),
		stats:   stat.NewTracker(clock),
		seq:     task.NewSource(0),
	}
	s.cond.L = &s.mu
	return s
}

// AddTenant 注册租户；cap<=0 表示不限队列长度。
func (s *Scheduler) AddTenant(id string, weight float64, cap int) error {
	t, err := tenant.New(id, weight)
	if err != nil {
		return admit.ErrBadWeight
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tenants[id] = t
	s.adm.Register(id, cap)
	s.stats.Add(id, weight)
	return nil
}

// Submit 以入队锁串行化顺序确定序号并入队；队列满时拒绝且不影响其他租户。
func (s *Scheduler) Submit(id string, cost float64) (task.Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return task.Task{}, ErrClosed
	}
	t, ok := s.tenants[id]
	if !ok {
		return task.Task{}, admit.ErrNoSuchTenant
	}
	if err := s.adm.Allow(id, t.Len()); err != nil {
		return task.Task{}, err
	}
	wasEmpty := t.Len() == 0
	tk := s.seq.Issue(id, cost)
	t.Enqueue(tk)
	if wasEmpty {
		t.RaiseTo(s.systemVT(t))
		s.heap.push(t)
	}
	s.cond.Signal()
	return tk, nil
}

// systemVT 是租户重新加入时的抬升下限：非空堆顶 vt，否则全表最大 vt。
func (s *Scheduler) systemVT(rejoin *tenant.Tenant) float64 {
	if s.heap.len() > 0 {
		return s.heap.items[0].VT()
	}
	floor := rejoin.VT()
	for _, t := range s.tenants {
		if t.VT() > floor {
			floor = t.VT()
		}
	}
	return floor
}

// Next 阻塞取下一个任务；关闭且排空后返回 ErrClosed。
func (s *Scheduler) Next() (task.Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for s.heap.len() == 0 && !s.closed {
		s.cond.Wait()
	}
	return s.popLocked()
}

// TryNext 非阻塞取任务；无任务可用时返回 ok=false。
func (s *Scheduler) TryNext() (task.Task, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.heap.len() == 0 {
		if s.closed {
			return task.Task{}, false, ErrClosed
		}
		return task.Task{}, false, nil
	}
	tk, err := s.popLocked()
	return tk, err == nil, err
}

func (s *Scheduler) popLocked() (task.Task, error) {
	if s.heap.len() == 0 {
		return task.Task{}, ErrClosed
	}
	s.heap.cmps = 0
	t := s.heap.pop()
	cmps := s.heap.cmps
	tk, _ := t.Dequeue()
	t.Advance(tk.Cost)
	if t.Len() > 0 {
		s.heap.push(t)
	}
	s.stats.Record(t.ID(), tk.Cost, cmps)
	return tk, nil
}

// RemoveTenant 移除租户；仍有排队任务时拒绝。
func (s *Scheduler) RemoveTenant(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tenants[id]
	if !ok {
		return admit.ErrNoSuchTenant
	}
	if t.Len() > 0 {
		return admit.ErrQueueNotEmpty
	}
	s.heap.remove(t)
	delete(s.tenants, id)
	s.adm.Forget(id)
	return nil
}

// Close 阻止新提交并在队列排空后唤醒所有等待者。
func (s *Scheduler) Close() {
	s.mu.Lock()
	s.closed = true
	s.cond.Broadcast()
	s.mu.Unlock()
}

// Snapshot 返回统计快照。
func (s *Scheduler) Snapshot() stat.Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stats.Snapshot()
}

// LastComparisons 返回单次选择的比较计数（供确定性单线程测试）。
func (s *Scheduler) LastComparisons() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stats.LastComparisons()
}
