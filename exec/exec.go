// Package exec 按依赖并行执行任务，处理结果收集、失败传播与取消。
package exec

import (
	"context"
	"fmt"
	"sync"
	"time"

	"ontology/fail"
	"ontology/graph"
	"ontology/sched"
)

// Fn 是任务执行函数。ctx 在快速失败时取消；尽力而为模式下不会被取消。
type Fn func(ctx context.Context) error

// TaskResult 是一个任务的最终记录。
type TaskResult struct {
	ID      string
	Status  fail.Status
	Err     error
	Started bool
}

// Engine 执行一张任务图。
type Engine struct {
	g   *graph.Graph
	fns map[string]Fn
	md  fail.Mode
	max int
}

// Option 配置引擎。
type Option func(*Engine)

// WithMode 设置失败传播模式。
func WithMode(m fail.Mode) Option { return func(e *Engine) { e.md = m } }

// WithConcurrency 设置并发上限（<1 视为 1）。
func WithConcurrency(n int) Option { return func(e *Engine) { e.max = n } }

// New 创建引擎；每个图中任务都必须在 fns 中提供执行函数。
func New(g *graph.Graph, fns map[string]Fn, opts ...Option) *Engine {
	e := &Engine{g: g, fns: fns, md: fail.FailFast, max: 8}
	for _, o := range opts {
		o(e)
	}
	return e
}

type nodeState struct {
	status        fail.Status
	err           error
	done, started bool
}

type runner struct {
	e        *Engine
	s        *sched.Scheduler
	states   map[string]*nodeState
	indeg    map[string]int
	mu       sync.Mutex
	wg       sync.WaitGroup
	finished int
	aborted  bool
	ctx      context.Context
	cancel   context.CancelFunc
}

func (r *runner) tasks() []string { return r.e.g.Tasks() }

func (r *runner) firstFailRoot() string {
	for _, id := range r.tasks() {
		if s := r.states[id]; s.done && s.status == fail.Failed {
			return id
		}
	}
	return ""
}

// setTerminal 调用方持锁：终态一次性写定，不可改写。
func (r *runner) setTerminal(id string, st fail.Status, err error, started bool) {
	ns := r.states[id]
	if ns.done {
		return
	}
	ns.done, ns.status, ns.err, ns.started = true, st, err, started
	r.finished++
}

// markSkip 持锁调用：未开始任务记 Skipped 并沿边传播，root 始终指最初失败。
func (r *runner) markSkip(id, root string) {
	if r.states[id].done {
		return
	}
	r.setTerminal(id, fail.Skipped, fail.RootCause(root), false)
	for _, v := range r.e.g.Successors(id) {
		r.markSkip(v, root)
	}
}

// abort 持锁调用：快速失败传播。
func (r *runner) abort(root string) {
	if r.aborted {
		return
	}
	r.aborted = true
	r.cancel()
	for k := len(r.s.Launch()); k > 0; k-- {
		id := <-r.s.Launch()
		r.markSkip(id, root)
	}
	for _, id := range r.s.DrainReady() {
		r.markSkip(id, root)
	}
}

func (r *runner) launch(id string) {
	r.mu.Lock()
	if r.states[id].done {
		r.mu.Unlock()
		r.s.Drop() // abort 已标记：丢弃派发并归还槽位（任务从未开始）。
		return
	}
	r.states[id].started = true
	fn := r.e.fns[id]
	r.mu.Unlock()

	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		var err error
		func() {
			defer func() {
				if rec := recover(); rec != nil {
					err = &fail.PanicError{TaskID: id, Value: rec}
				}
			}()
			err = fn(r.ctx)
		}()
		r.s.Finish(id)
		r.mu.Lock()
		defer r.mu.Unlock()
		ns := r.states[id]
		if ns.done {
			return // 已被标记 Canceled：迟到回写丢弃，状态不改。
		}
		if r.aborted {
			r.setTerminal(id, fail.Canceled, fail.RootCause(r.firstFailRoot()), true)
			return
		}
		if err != nil {
			r.setTerminal(id, fail.Failed, err, true)
			for _, v := range r.e.g.Successors(id) {
				r.markSkip(v, id)
			}
			if r.e.md == fail.FailFast {
				r.abort(id)
				for _, x := range r.tasks() {
					if xs := r.states[x]; !xs.done && xs.started {
						r.setTerminal(x, fail.Canceled, fail.RootCause(id), true)
					}
				}
			}
			return
		}
		r.setTerminal(id, fail.Success, nil, true)
		for _, v := range r.e.g.Successors(id) {
			r.indeg[v]--
			if r.indeg[v] == 0 && !r.states[v].done {
				r.s.Enqueue(v)
			}
		}
	}()
}

// Run 执行全部任务，返回按 ID 升序的结果；图有环时返回 fail.ErrCycle。
// Run 返回前其启动的所有 goroutine 均已退出，无泄漏。
func (e *Engine) Run(parent context.Context) ([]TaskResult, error) {
	if cyc := e.g.Cycle(); cyc != nil {
		return nil, fmt.Errorf("%w: %v", fail.ErrCycle, cyc)
	}
	n := e.g.N()
	if n == 0 {
		return []TaskResult{}, nil
	}
	ctx, cancel := context.WithCancel(parent)
	r := &runner{
		e: e, s: sched.New(e.max, n), states: map[string]*nodeState{},
		indeg: map[string]int{}, ctx: ctx, cancel: cancel,
	}
	defer cancel()
	for _, id := range r.tasks() {
		r.states[id] = &nodeState{}
		r.indeg[id] = len(e.g.Predecessors(id))
		if r.indeg[id] == 0 {
			r.s.Enqueue(id)
		}
	}
	tick := time.NewTicker(2 * time.Millisecond)
	defer tick.Stop()
	for func() bool {
		r.mu.Lock()
		defer r.mu.Unlock()
		return r.finished < n
	}() {
		select {
		case id := <-r.s.Launch():
			r.launch(id)
		case <-r.s.Done():
			r.s.Release()
		case <-tick.C:
		}
	}
	r.s.CloseLaunch()
	r.wg.Wait()

	out := make([]TaskResult, 0, n)
	for _, id := range r.tasks() {
		ns := r.states[id]
		out = append(out, TaskResult{ID: id, Status: ns.status, Err: ns.err, Started: ns.started})
	}
	return out, nil
}
