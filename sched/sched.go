// Package sched 按依赖拓扑调度任务：限并发、就绪队列、传播挂钩。
package sched

import (
	"context"
	"sort"
	"sync"

	"ontology/fail"
	"ontology/graph"
)

// TaskFunc 是任务执行体。
type TaskFunc func(ctx context.Context) error

// Hooks 接收调度事件，由 exec 实现并负责状态写回。
type Hooks interface {
	OnStart(id string)
	OnResult(id string, err error, panicked bool)
	OnAbort(id string)
	// MarkSkipped 把一个从未启动、有效前置归零且有失败祖先的节点标为
	// skipped；返回是否为新标记（新标记继续级联）。
	MarkSkipped(id string) bool
}

// Scheduler 按拓扑就绪顺序执行任务。
type Scheduler struct {
	g       *graph.Graph
	tasks   map[string]TaskFunc
	hooks   Hooks
	maxConc int

	mu        sync.Mutex
	cond      *sync.Cond
	remaining map[string]int // 剩余未结束前置数
	queued    map[string]bool
	ready     []string
	running   int
	peak      int // 历史最大同时运行任务数
	decisions int // 就绪判定次数
	aborted   bool

	sem chan struct{}
	wg  sync.WaitGroup
}

func New(g *graph.Graph, tasks map[string]TaskFunc, hooks Hooks, maxConc int) *Scheduler {
	if maxConc < 1 {
		maxConc = 1
	}
	s := &Scheduler{
		g:         g,
		tasks:     tasks,
		hooks:     hooks,
		maxConc:   maxConc,
		remaining: map[string]int{},
		queued:    map[string]bool{},
		sem:       make(chan struct{}, maxConc),
	}
	s.cond = sync.NewCond(&s.mu)
	return s
}

func (s *Scheduler) Peak() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.peak
}

func (s *Scheduler) Decisions() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.decisions
}

// Abort 进入 FailFast 中止态：残留就绪任务仍启动以经历取消，但不执行函数体。
func (s *Scheduler) Abort() {
	s.mu.Lock()
	s.aborted = true
	s.cond.Broadcast()
	s.mu.Unlock()
}

// Run 运行直到全部任务结束。
func (s *Scheduler) Run(ctx context.Context) {
	for _, id := range s.g.Nodes() {
		s.remaining[id] = len(s.g.Preds(id))
		if s.remaining[id] == 0 {
			s.enqueueLocked(id)
		}
	}
	for {
		id, ok := s.takeLocked()
		if !ok {
			break
		}
		s.wg.Add(1)
		go s.runOne(ctx, id)
	}
	s.wg.Wait()
}

func (s *Scheduler) enqueueLocked(id string) {
	s.decisions++
	if s.queued[id] {
		return
	}
	s.queued[id] = true
	s.ready = append(s.ready, id)
	sort.Strings(s.ready) // 同时就绪按 ID 升序取
}

func (s *Scheduler) takeLocked() (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for len(s.ready) == 0 || s.running >= s.maxConc {
		if s.running == 0 && len(s.ready) == 0 {
			return "", false
		}
		s.cond.Wait()
	}
	id := s.ready[0]
	s.ready = s.ready[1:]
	s.running++
	if s.running > s.peak {
		s.peak = s.running
	}
	s.sem <- struct{}{}
	return id, true
}

func (s *Scheduler) runOne(ctx context.Context, id string) {
	defer s.wg.Done()
	defer func() { <-s.sem }()

	s.mu.Lock()
	aborted := s.aborted
	s.mu.Unlock()

	s.hooks.OnStart(id)
	if aborted {
		s.hooks.OnAbort(id)
		s.finish(id, false)
		return
	}
	var err error
	panicked := false
	func() {
		defer func() {
			if r := recover(); r != nil {
				err = fail.WrapPanic(r)
				panicked = true
			}
		}()
		err = s.tasks[id](ctx)
	}()
	s.hooks.OnResult(id, err, panicked)
	s.finish(id, err != nil)
}

// finish 统一处理一个节点结束后的计数释放与 BFS 传播。
// dead=true 表示该节点非成功终态（失败或被跳过路径的源头）。
func (s *Scheduler) finish(id string, dead bool) {
	s.mu.Lock()
	type step struct {
		id   string
		dead bool
	}
	queue := []step{{id, dead}}
	var newlySkipped []string
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, succ := range s.g.Succs(cur.id) {
			if s.queued[succ] || s.remaining[succ] <= 0 {
				continue
			}
			s.remaining[succ]--
			if s.remaining[succ] > 0 {
				continue
			}
			if cur.dead {
				s.remaining[succ] = -1
				newlySkipped = append(newlySkipped, succ)
				queue = append(queue, step{succ, true})
			} else {
				s.enqueueLocked(succ)
			}
		}
	}
	s.running--
	s.cond.Broadcast()
	s.mu.Unlock()

	for _, sk := range newlySkipped {
		s.hooks.MarkSkipped(sk)
	}
}
