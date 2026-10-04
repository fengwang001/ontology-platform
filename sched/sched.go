// Package sched 实现带并发组的流水线运行调度器。
//
// 全部公开操作在同一把互斥锁内完成，锁内即线性化点，因此并发调用
// 等价于某个确定的串行顺序。每次操作末尾统一调用 allocate 分配执行位。
package sched

import (
	"errors"
	"sync"

	"ontology/group"
	"ontology/slot"
)

// 四类可被 errors.Is 区分的错误。
var (
	// ErrInvalid：参数非法（C/Q 越界、组名过长、运行编号非正）。
	ErrInvalid = errors.New("sched: invalid argument")
	// ErrNotFound：运行不存在。
	ErrNotFound = errors.New("sched: run not found")
	// ErrState：运行状态与操作不符。
	ErrState = errors.New("sched: state mismatch")
	// ErrQueueFull：Submit 的队列净增会超过 Q。
	ErrQueueFull = errors.New("sched: waiting queue full")
)

// Scheduler 是运行调度器。
type Scheduler struct {
	mu        sync.Mutex
	runs      map[int]*group.Run
	table     *group.Table
	pool      *slot.Pool
	qcap      int
	nextID    int
	touched   map[int]struct{}
	peakQueue int
}

// New 创建调度器：C 为全局执行位数（1..10000），Q 为等待队列上限（0..100000）。
func New(C, Q int) (*Scheduler, error) {
	if C < 1 || C > 10000 || Q < 0 || Q > 100000 {
		return nil, ErrInvalid
	}
	return &Scheduler{
		runs:    make(map[int]*group.Run),
		table:   group.NewTable(),
		pool:    slot.New(C),
		qcap:    Q,
		nextID:  1,
		touched: make(map[int]struct{}),
	}, nil
}

// begin 进入一次操作：加锁并重置本次操作的 touched 集合。
func (s *Scheduler) begin() {
	s.mu.Lock()
	s.touched = make(map[int]struct{})
	s.peakQueue = 0
}

// touch 记录本次操作读写过的一条运行记录（按不同运行 ID 去重）。
func (s *Scheduler) touch(r *group.Run) {
	if r != nil {
		s.touched[r.ID] = struct{}{}
	}
}

// end 结束操作：解锁并返回本次触达的不同运行记录数。
func (s *Scheduler) end() int {
	n := len(s.touched)
	s.mu.Unlock()
	return n
}

// allocate 在操作末尾按 FIFO 反复把队首提升为 Running，直到无空位或队列空。
func (s *Scheduler) allocate() {
	for s.pool.HasFree() {
		id, ok := s.pool.Pop()
		if !ok {
			return
		}
		r := s.runs[id]
		s.touch(r)
		r.SetQueued(false)
		r.State = group.Running
		s.pool.Acquire()
	}
}

// promote 把本组 Pending 晋升为占位者并排入等待队列尾（不受 Q 约束）。
func (s *Scheduler) promote(g []byte) {
	_, pending := s.table.Entry(g)
	if pending == nil {
		return
	}
	s.touch(pending)
	s.table.SetPending(g, nil)
	pending.State = group.Waiting
	pending.SetQueued(true)
	s.table.SetHolder(g, pending)
	s.pool.Push(pending.ID)
	if n := s.pool.Len(); n > s.peakQueue {
		s.peakQueue = n
	}
}

// Submit 提交一次运行，返回运行编号（编号 1,2,3…，被拒不占号）。
// cancelInProgress 决定是否顶替组内占位者；protected 仅保护已开始的本运行
// 不被后来的 Submit 顶替，不保护 Waiting，也不挡用户显式 Cancel。
// 空组名表示不属于任何组，每次提交互相独立。
func (s *Scheduler) Submit(g []byte, cancelInProgress, protected bool) (int, error) {
	if len(g) > 64 {
		return 0, ErrInvalid
	}
	s.begin()
	defer s.end()

	var holder, pending *group.Run
	if len(g) > 0 {
		holder, pending = s.table.Entry(g)
		if holder != nil {
			s.touch(holder)
		}
		if pending != nil {
			s.touch(pending)
		}
	}

	// 队列上限的净增判定：完成入参与末尾分配后，队列长度相对提交前
	// 严格净增时才可能超限。唯一会净增的情形是「新运行成为占位者并入队，
	// 且当前无空闲执行位」。顶替 Waiting 为一出一入（净增 0）；成为
	// Pending 从不过队列门；有空位时入队后立即 Running（L1==L0）。
	if holder == nil && !s.pool.HasFree() && s.pool.Len() >= s.qcap {
		return 0, ErrQueueFull
	}

	id := s.nextID
	s.nextID++
	r := group.NewRun(id, append([]byte(nil), g...), protected)
	s.runs[id] = r
	s.touch(r)

	if holder == nil {
		r.State = group.Waiting
		r.SetQueued(true)
		if len(g) > 0 {
			s.table.SetHolder(g, r)
		}
		s.pool.Push(id)
		s.allocate()
		return id, nil
	}

	// 组内已有占位者：旧 Pending（若有）立即被新提交顶替。
	if pending != nil {
		pending.State = group.Superseded
		s.table.SetPending(g, nil)
	}

	if !cancelInProgress {
		r.State = group.Pending
		s.table.SetPending(g, r)
		s.allocate()
		return id, nil
	}

	switch holder.State {
	case group.Waiting:
		// protected 不保护 Waiting：占位者立即取消并 O(1) 出队。
		holder.State = group.Cancelled
		holder.SetQueued(false)
		s.pool.Remove(holder.ID)
		s.table.SetHolder(g, nil) // 此分支仅在 len(g)>0 时可达
		r.State = group.Waiting
		r.SetQueued(true)
		s.table.SetHolder(g, r)
		s.pool.Push(id)
		s.allocate()
	case group.Running:
		if holder.Protected() {
			r.State = group.Pending
			s.table.SetPending(g, r)
		} else {
			holder.State = group.Cancelling // 仍占执行位、仍为占位者
			r.State = group.Pending
			s.table.SetPending(g, r)
		}
		s.allocate()
	default: // group.Cancelling：占位者不变，新运行为 Pending。
		r.State = group.Pending
		s.table.SetPending(g, r)
		s.allocate()
	}
	return id, nil
}

// Finish 结束运行。Running 按 ok 转 Succeeded/Failed；对 Cancelling 调用也接受，
// 但一律转 Cancelled（取消优先，ok 丢弃）。
func (s *Scheduler) Finish(id int, ok bool) error {
	if id <= 0 {
		return ErrInvalid
	}
	s.begin()
	defer s.end()

	r := s.runs[id]
	if r == nil {
		return ErrNotFound
	}
	s.touch(r)
	switch r.State {
	case group.Running:
		if ok {
			r.State = group.Succeeded
		} else {
			r.State = group.Failed
		}
	case group.Cancelling:
		r.State = group.Cancelled
	default:
		return ErrState
	}

	g := r.Group
	s.pool.Release()
	s.table.SetHolder(g, nil)
	s.promote(g)
	s.allocate()
	return nil
}

// AckCancel 确认取消：仅 Cancelling 可调，转 Cancelled 并让出执行位。
func (s *Scheduler) AckCancel(id int) error {
	if id <= 0 {
		return ErrInvalid
	}
	s.begin()
	defer s.end()

	r := s.runs[id]
	if r == nil {
		return ErrNotFound
	}
	s.touch(r)
	if r.State != group.Cancelling {
		return ErrState
	}
	r.State = group.Cancelled

	g := r.Group
	s.pool.Release()
	s.table.SetHolder(g, nil)
	s.promote(g)
	s.allocate()
	return nil
}

// Cancel 发起取消。Pending/Waiting 立即 Cancelled（Waiting O(1) 出队）；
// Running 转 Cancelling（protected 不挡用户取消）；对 Cancelling 重复调用
// 报 ErrState。
func (s *Scheduler) Cancel(id int) error {
	if id <= 0 {
		return ErrInvalid
	}
	s.begin()
	defer s.end()

	r := s.runs[id]
	if r == nil {
		return ErrNotFound
	}
	s.touch(r)
	switch r.State {
	case group.Pending:
		r.State = group.Cancelled
		s.table.SetPending(r.Group, nil)
		s.allocate()
		return nil
	case group.Waiting:
		r.State = group.Cancelled
		r.SetQueued(false)
		s.pool.Remove(id)
		s.table.SetHolder(r.Group, nil)
		s.allocate()
		return nil
	case group.Running:
		r.State = group.Cancelling
		s.allocate()
		return nil
	default:
		return ErrState
	}
}
