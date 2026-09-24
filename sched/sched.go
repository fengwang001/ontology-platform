// Package sched 按依赖并行调度任务图，支持快速失败与尽力而为两种模式。
package sched

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync/atomic"

	"ontology/exec"
	"ontology/fail"
	"ontology/graph"
	"ontology/report"
)

// Options 控制调度行为。
type Options struct {
	Concurrency int  // 同时运行任务数上限，<1 时归一为 1
	BestEffort  bool // true 时不依赖失败任务的分支继续跑完
}

// CycleError 在执行前检出环时返回，Path 为首尾闭合的真实环路径。
type CycleError struct{ Path []string }

func (e *CycleError) Error() string {
	return "sched: cycle detected: " + strings.Join(e.Path, " -> ")
}

// Engine 绑定任务图与任务函数，并统计并发峰值与就绪判定次数。
type Engine struct {
	g     *graph.Graph
	tasks map[string]exec.Func
	opts  Options

	running     atomic.Int64
	peak        atomic.Int64
	readyChecks atomic.Int64
}

// New 校验图（含执行前环检测）与任务函数后创建调度器。
func New(g *graph.Graph, tasks map[string]exec.Func, opts Options) (*Engine, error) {
	if _, cyc := g.Layers(); cyc != nil {
		return nil, &CycleError{Path: cyc}
	}
	for id := range tasks {
		if !g.Has(id) {
			return nil, fmt.Errorf("%w: %s", graph.ErrUnknownNode, id)
		}
	}
	if opts.Concurrency < 1 {
		opts.Concurrency = 1
	}
	return &Engine{g: g, tasks: tasks, opts: opts}, nil
}

// Peak 返回历史最大同时运行任务数。
func (e *Engine) Peak() int64 { return e.peak.Load() }

// ReadyChecks 返回就绪判定总次数，设计上界为 N+E。
func (e *Engine) ReadyChecks() int64 { return e.readyChecks.Load() }

func (e *Engine) addRun() {
	n := e.running.Add(1)
	for {
		if p := e.peak.Load(); n <= p || e.peak.CompareAndSwap(p, n) {
			return
		}
	}
}

// rootCause 在 id 的前驱中找失败序号最小（并列取 ID 最小）的失败根因。
func rootCause(g *graph.Graph, id string, state map[string]fail.State,
	cause map[string]string, failOrd map[string]int) (string, bool) {
	best := ""
	found := false
	for _, p := range g.Predecessors(id) {
		var root string
		switch state[p] {
		case fail.Failed:
			root = p
		case fail.Skipped:
			root = cause[p]
		default:
			continue
		}
		if !found || failOrd[root] < failOrd[best] ||
			(failOrd[root] == failOrd[best] && root < best) {
			best, found = root, true
		}
	}
	return best, found
}

// Run 执行整张图并返回确定性报告；返回时所有任务 goroutine 已退出。
func (e *Engine) Run(ctx context.Context) *report.Report {
	nodes := e.g.Nodes()
	rep := report.New()
	state := make(map[string]fail.State, len(nodes))
	cause := make(map[string]string, len(nodes))
	failOrd := make(map[string]int, len(nodes))
	indeg := make(map[string]int, len(nodes))
	for _, id := range nodes {
		indeg[id] = len(e.g.Predecessors(id))
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	results := make(chan exec.Result)
	finalized := 0
	finalize := func(id string, st fail.State, err error, c string) {
		state[id] = st
		cause[id] = c
		rep.Set(report.Entry{ID: id, State: st, Err: err, Cause: c})
		finalized++
	}
	var ready []string // 始终保持 ID 升序，队首即最小
	insertReady := func(id string) {
		i := sort.SearchStrings(ready, id)
		ready = append(ready, "")
		copy(ready[i+1:], ready[i:])
		ready[i] = id
	}
	var release func(id string) // 前驱落定后迭代释放后继（显式栈，无递归）
	release = func(id string) {
		stack := []string{id}
		for len(stack) > 0 {
			cur := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			for _, s := range e.g.Successors(cur) {
				if state[s] != fail.Pending {
					continue
				}
				e.readyChecks.Add(1)
				indeg[s]--
				if indeg[s] > 0 {
					continue
				}
				if root, bad := rootCause(e.g, s, state, cause, failOrd); bad {
					finalize(s, fail.Skipped, nil, root)
					stack = append(stack, s)
				} else {
					insertReady(s)
				}
			}
		}
	}
	for _, id := range nodes {
		e.readyChecks.Add(1)
		if indeg[id] == 0 {
			ready = append(ready, id) // nodes 已有序，ready 亦有序
		}
	}
	aborted := false
	failSeq := 0
	inFlight := 0
	for finalized < len(nodes) {
		for inFlight < e.opts.Concurrency && len(ready) > 0 {
			id := ready[0]
			ready = ready[1:]
			state[id] = fail.Running
			inFlight++
			e.addRun()
			fn := e.tasks[id]
			if fn == nil {
				fn = func(context.Context) error { return errors.New("sched: missing task func") }
			}
			go func() {
				defer e.running.Add(-1)
				results <- exec.Run(ctx, id, fn)
			}()
		}
		if inFlight == 0 {
			break
		}
		res := <-results
		inFlight--
		id := res.ID
		genuineErr := res.Err != nil && !errors.Is(res.Err, context.Canceled)
		switch {
		case aborted && !genuineErr:
			finalize(id, fail.Canceled, nil, "") // 取消后写回的结果被丢弃
		case res.Err == nil:
			finalize(id, fail.Succeeded, nil, "")
			release(id)
		default:
			failSeq++
			failOrd[id] = failSeq
			finalize(id, fail.Failed, res.Err, "")
			if e.opts.BestEffort || aborted {
				release(id)
			} else {
				aborted = true
				cancel()
				for _, n := range nodes { // 快速失败：全部未启动任务跳过
					if state[n] == fail.Pending {
						finalize(n, fail.Skipped, nil, id)
					}
				}
				ready = nil
			}
		}
	}
	rep.Finalize()
	return rep
}
