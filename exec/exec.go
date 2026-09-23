// Package exec 是按键串行、全局限并发、键间公平的任务执行器。
// n 个 worker 从就绪键轮转环取键，每次只执行该键的一个任务后把键放回环尾。
package exec

import (
	"errors"
	"sync"

	"ontology/keyq"
	"ontology/ring"
)

var (
ErrClosed     = errors.New("exec: executor closed")
ErrOverloaded = errors.New("exec: pending queue overloaded")
)

// MaxPending 是全局「已提交未完成」任务数上限。
const MaxPending = 1 << 20

type task struct{ fn func() }

// Stats 是执行器的可观测计数快照。
type Stats struct {
	Done, Failed, Pending, InFlight int
	MaxConcurrency                  int
}

// Executor 调度并执行任务。零值不可用，请用 New。
type Executor struct {
	mu      sync.Mutex
	cond    *sync.Cond
	q       *keyq.Queues[string, task]
	rr      *ring.Ring[string]
	running map[string]bool
	n       int
	pending int
	inFlight, maxConc, done, failed int
	closed  bool
	wg      sync.WaitGroup
}

func New(n int) *Executor {
	if n < 1 {
		n = 1
	}
	e := &Executor{
		q:       keyq.New[string, task](),
		rr:      ring.New[string](),
		running: make(map[string]bool),
		n:       n,
	}
	e.cond = sync.NewCond(&e.mu)
	for range n {
		e.wg.Add(1)
		go e.worker()
	}
	return e
}

// Submit 把 fn 排到 key 的队尾。关闭后返回 ErrClosed；超容量返回 ErrOverloaded 且无副作用。
func (e *Executor) Submit(key string, fn func()) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return ErrClosed
	}
	if e.pending >= MaxPending {
		return ErrOverloaded
	}
	e.pending++
	wasEmpty := e.q.Push(key, task{fn})
	if wasEmpty && !e.running[key] {
		e.rr.Add(key)
		e.cond.Broadcast()
	}
	return nil
}

func (e *Executor) worker() {
	defer e.wg.Done()
	e.mu.Lock()
	for {
		for e.rr.Len() == 0 {
			if e.closed {
				return
			}
			e.cond.Wait()
		}
		key, _ := e.rr.Pop()
		t, ok := e.q.Pop(key)
		if !ok {
			continue
		}
		e.running[key] = true
		e.pending--
		e.inFlight++
		if e.inFlight > e.maxConc {
			e.maxConc = e.inFlight
		}
		e.mu.Unlock()

		e.runOne(t)

		e.mu.Lock()
		e.inFlight--
		e.done++
		if e.q.Len(key) > 0 {
			e.rr.Add(key) // 键仍有任务：回环尾，交给可能不同的 worker（公平）
			e.cond.Broadcast()
		} else {
			delete(e.running, key)
			if e.pending == 0 && e.inFlight == 0 && e.rr.Len() == 0 {
				e.cond.Broadcast()
			}
		}
	}
}

func (e *Executor) runOne(t task) {
	defer func() {
		if r := recover(); r != nil {
			e.mu.Lock()
			e.failed++
			e.mu.Unlock()
		}
	}()
	t.fn()
}

// Wait 阻塞直到当前已提交的任务全部完成。
func (e *Executor) Wait() {
	e.mu.Lock()
	for e.pending > 0 || e.inFlight > 0 {
		e.cond.Wait()
	}
	e.mu.Unlock()
}

// Close 后 Submit 返回 ErrClosed；待已提交任务全部执行完才返回，worker 全部退出。
func (e *Executor) Close() {
	e.mu.Lock()
	e.closed = true
	e.cond.Broadcast()
	for e.pending > 0 || e.inFlight > 0 {
		e.cond.Wait()
	}
	e.mu.Unlock()
	e.wg.Wait()
}

// Stats 返回计数快照。
func (e *Executor) Stats() Stats {
	e.mu.Lock()
	defer e.mu.Unlock()
	return Stats{Done: e.done, Failed: e.failed, Pending: e.pending,
		InFlight: e.inFlight, MaxConcurrency: e.maxConc}
}
