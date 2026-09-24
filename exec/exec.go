// Package exec 把图、状态机与调度器编排成一次完整运行。
package exec

import (
	"context"

	"ontology/fail"
	"ontology/graph"
	"ontology/sched"
)

// Mode 选择失败传播模式。
type Mode int

const (
	FailFast Mode = iota
	BestEffort
)

// TaskMap 是任务 ID 到执行体的映射。
type TaskMap map[string]sched.TaskFunc

// Result 持有一次运行的全部终态。
type Result struct {
	States []fail.State
}

// Engine 编排 graph + tracker + scheduler。
type Engine struct {
	g        *graph.Graph
	tasks    TaskMap
	mode     Mode
	conc     int
	sched    *sched.Scheduler
	tr       *fail.Tracker
	cancelFn context.CancelFunc
	ctxDone  <-chan struct{}
}

func New(g *graph.Graph, tasks TaskMap, mode Mode, conc int) *Engine {
	return &Engine{g: g, tasks: tasks, mode: mode, conc: conc}
}

func (e *Engine) Scheduler() *sched.Scheduler { return e.sched }
func (e *Engine) Tracker() *fail.Tracker      { return e.tr }

func (e *Engine) cancel() {
	if e.cancelFn != nil {
		e.cancelFn()
	}
}

func (e *Engine) canceled() bool {
	if e.ctxDone == nil {
		return false
	}
	select {
	case <-e.ctxDone:
		return true
	default:
		return false
	}
}

type hooks struct {
	tr     *fail.Tracker
	engine *Engine
}

func (h *hooks) OnStart(id string) { h.tr.MarkStarted(id) }

func (h *hooks) OnResult(id string, err error, panicked bool) {
	if h.tr.StateOf(id).Status == fail.StatusCanceled {
		// 取消后迟到的结果：丢弃，保持 canceled。
		return
	}
	if err != nil {
		if h.engine.mode == FailFast && h.engine.canceled() && !panicked {
			// 任务是在全局取消后带着 ctx.Err 返回的：属于被取消而非自身失败。
			h.tr.CancelOne(id)
			return
		}
		h.tr.Complete(id, fail.StatusFailed, err)
		if h.engine.mode == FailFast {
			h.tr.CancelPending(true)
			h.engine.cancel()
			h.engine.sched.Abort()
		}
		return
	}
	h.tr.Complete(id, fail.StatusSuccess, nil)
}

func (h *hooks) OnAbort(id string) {
	// FailFast 中止后启动的旁支：与失败点无失败祖先，全局取消 -> canceled。
	h.tr.CancelOne(id)
}

func (h *hooks) MarkSkipped(id string) bool {
	return h.tr.MarkSkipped(id)
}

// Run 执行前做环检测；调用方应先 graph.Validate，这里再兜底一次。
func (e *Engine) Run(ctx context.Context) (*Result, error) {
	if err := e.g.Validate(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	e.ctxDone = ctx.Done()
	e.tr = fail.NewTracker(e.g)
	h := &hooks{tr: e.tr, engine: e}
	e.sched = sched.New(e.g, e.tasks, h, e.conc)
	e.cancelFn = cancel
	e.sched.Run(ctx)
	return &Result{States: e.tr.Snapshot()}, nil
}
