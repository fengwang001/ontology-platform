package approval

import (
	"sync"

	"ontology/deadline"
	"ontology/org"
)

const (
	maxAmount = 1_000_000_000_000
	maxNow    = 1_000_000_000_000_000
	maxT      = 1_000_000_000
)

// Engine 是审批流引擎；org 与 approval 操作均可并发。
type Engine struct {
	mu       sync.RWMutex
	org      *org.Org
	timeout  int64
	clock    int64
	requests map[string]*request
	heap     *deadline.Heap
}

// New 创建引擎；T 为每级超时时长（1..1e9）。
func New(o *org.Org, T int64) (*Engine, error) {
	if T < 1 || T > maxT || o == nil {
		return nil, ErrInvalid
	}
	return &Engine{
		org:      o,
		timeout:  T,
		requests: make(map[string]*request),
		heap:     deadline.NewHeap(),
	}, nil
}

// Submit 提交申请并冻结审批链。
func (e *Engine) Submit(req, a string, amount, now int64) error {
	if req == "" || a == "" || amount < 1 || amount > maxAmount || now < 0 || now > maxNow {
		return ErrInvalid
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if now < e.clock {
		return ErrClock
	}
	changes := e.expire(now)
	if _, ok := e.requests[req]; ok {
		changes.rollback(e)
		return ErrNotFound
	}
	candidates := make([]string, 0)
	for _, m := range e.org.Chain(a) {
		if e.org.Limit(m) >= amount {
			candidates = append(candidates, m)
		}
	}
	if len(candidates) == 0 {
		changes.rollback(e)
		return ErrNoApprover
	}
	r := &request{
		applicant:  a,
		amount:     amount,
		candidates: candidates,
		ta:         now,
	}
	e.requests[req] = r
	e.heap.Push(req, r.ta+e.timeout)
	e.clock = now
	return nil
}

// Decide 由当前审批人按当前授权实时校验后作出决策。
func (e *Engine) Decide(req, who string, ok bool, now int64) error {
	if req == "" || who == "" || now < 0 || now > maxNow {
		return ErrInvalid
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if now < e.clock {
		return ErrClock
	}
	changes := e.expire(now)
	r, ok2 := e.requests[req]
	if !ok2 {
		changes.rollback(e)
		return ErrNotFound
	}
	if r.outcome != Pending {
		changes.rollback(e)
		return ErrClosed
	}
	if who != r.assignee() {
		changes.rollback(e)
		return ErrNotAssignee
	}
	if e.org.Limit(who) < r.amount {
		changes.rollback(e)
		return ErrRevoked
	}
	if ok {
		r.outcome = Approved
	} else {
		r.outcome = Rejected
	}
	r.finalAt = now
	e.heap.Remove(req)
	e.clock = now
	return nil
}

// Status 返回按 now 做虚拟到期处理后的只读状态，不推进时钟。
func (e *Engine) Status(req string, now int64) (Status, error) {
	if req == "" || now < 0 || now > maxNow {
		return Status{}, ErrInvalid
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if now < e.clock {
		return Status{}, ErrClock
	}
	changes := e.expire(now)
	defer changes.rollback(e)
	r, ok := e.requests[req]
	if !ok {
		return Status{}, ErrNotFound
	}
	st := Status{Outcome: r.outcome, Ta: r.ta, FinalAt: r.finalAt}
	if r.outcome == Pending {
		st.Assignee = r.assignee()
	} else {
		st.Ta = r.finalAt
	}
	return st, nil
}

// Examined 返回最近一次成功/被拒操作中到期处理考察的堆项数。
func (e *Engine) Examined() int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.heap.Examined()
}

// changes 是一次到期处理产生的、可整体撤销的效果。
type changes struct {
	touched map[string]snapshot
	popped  []*deadline.Item
}

// expire 在当前状态上做到期处理（调用方持锁）。
// 采用两阶段：先反复从堆顶考察，收集每个受影响申请的终态结果，
// 同时暂存堆变更（升级重排/终局删除）与申请快照；成功时由调用方
// 直接保留这些变更，失败时调用 rollback 完整恢复堆与申请。
func (e *Engine) expire(now int64) changes {
	c := changes{touched: make(map[string]snapshot)}
	e.heap.BeginDrain()
	for {
		top := e.heap.Peek()
		e.heap.Examine()
		if top == nil || top.Due > now {
			return c
		}
		e.heap.Pop()
		c.popped = append(c.popped, top)
		r := e.requests[top.Req]
		if _, seen := c.touched[top.Req]; !seen {
			c.touched[top.Req] = r.snapshotState()
		}
		r.advance(top.Due)
		if r.outcome == Pending {
			e.heap.Push(top.Req, r.ta+e.timeout)
		}
	}
}

// rollback 撤销到期处理对堆与申请状态的全部修改（逆序恢复）。
func (c changes) rollback(e *Engine) {
	oldDue := make(map[string]int64, len(c.touched))
	for req, s := range c.touched {
		oldDue[req] = s.ta + e.timeout
		e.requests[req].restore(s)
	}
	e.heap.Restore(c.popped, oldDue)
}
