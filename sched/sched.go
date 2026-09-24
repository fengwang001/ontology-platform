// Package sched 按依赖并行调度任务图，并在失败时传播取消与跳过。
package sched

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"ontology/exec"
	"ontology/fail"
	"ontology/graph"
	"ontology/report"
)

// Mode 决定失败后的调度策略。
type Mode int

const (
	// FailFast 默认：首个失败即取消在跑任务、跳过未跑任务。
	FailFast Mode = iota
	// BestEffort 尽力而为：不取消，不依赖失败任务的分支继续跑完。
	BestEffort
)

// Engine 调度一张任务图。计数器非导出，经 Peak/ReadyChecks 读取。
type Engine struct {
	g     *graph.Graph
	fns   map[string]exec.Func
	limit int
	mode  Mode

	peak        int
	readyChecks int
}

// New 构造调度器；limit < 1 时按 1 处理（退化为串行）。
func New(g *graph.Graph, fns map[string]exec.Func, limit int, mode Mode) *Engine {
	if limit < 1 {
		limit = 1
	}
	return &Engine{g: g, fns: fns, limit: limit, mode: mode}
}

// Peak 返回历史最大同时运行任务数。
func (e *Engine) Peak() int { return e.peak }

// ReadyChecks 返回就绪判定总次数（初始化每节点一次 + 每条已松弛边一次）。
func (e *Engine) ReadyChecks() int { return e.readyChecks }

type outcome struct {
	id  string
	err error
}

// Run 执行整张图；有环时不执行任何任务，返回包装 fail.ErrCycle 的错误。
func (e *Engine) Run(ctx context.Context) (*report.Report, error) {
	if cyc := e.g.Cycle(); cyc != nil {
		return nil, fmt.Errorf("%w: %s", fail.ErrCycle, strings.Join(cyc, "->"))
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	nodes := e.g.Nodes()
	indeg := make(map[string]int, len(nodes))
	var ready []string
	for _, id := range nodes {
		indeg[id] = len(e.g.Preds(id))
		e.readyChecks++
		if indeg[id] == 0 {
			ready = insert(ready, id)
		}
	}

	results := make(chan outcome)
	running := 0
	inflight := map[string]bool{}
	succeeded := map[string]bool{}
	failed := map[string]error{}
	canceledSet := map[string]bool{}
	trigger := ""

	dispatch := func() {
		for running < e.limit && len(ready) > 0 && trigger == "" {
			id := ready[0]
			ready = ready[1:]
			fn := e.fns[id]
			inflight[id] = true
			running++
			if running > e.peak {
				e.peak = running
			}
			go func() { results <- outcome{id, exec.Run(ctx, fn)} }()
		}
	}

	for dispatch(); running > 0; dispatch() {
		res := <-results
		running--
		delete(inflight, res.id)
		if canceledSet[res.id] {
			// 取消后的迟到写回一律丢弃；真实错误优先，改记 Failed。
			if res.err != nil && !errors.Is(res.err, context.Canceled) {
				failed[res.id] = res.err
				delete(canceledSet, res.id)
			}
			continue
		}
		if res.err != nil {
			failed[res.id] = res.err
			if e.mode == FailFast && trigger == "" {
				trigger = res.id
				cancel()
				for id := range inflight {
					canceledSet[id] = true
				}
			}
			continue
		}
		succeeded[res.id] = true
		for _, s := range e.g.Succs(res.id) {
			indeg[s]--
			e.readyChecks++
			if indeg[s] == 0 {
				ready = insert(ready, s)
			}
		}
	}

	rep := report.New()
	for _, id := range nodes {
		switch {
		case succeeded[id]:
			rep.Results[id] = report.Result{State: fail.Succeeded}
		case failed[id] != nil:
			rep.Results[id] = report.Result{State: fail.Failed, Err: failed[id]}
		case canceledSet[id]:
			rep.Results[id] = report.Result{State: fail.Canceled, Cause: trigger, Err: fail.ErrCanceled}
		default:
			cause := e.rootCause(id, failed)
			if cause == "" {
				cause = trigger
			}
			rep.Results[id] = report.Result{State: fail.Skipped, Cause: cause, Err: fail.ErrSkipped}
		}
	}
	return rep, nil
}

// rootCause 迭代 DFS 收集 id 的失败祖先，返回字典序最小者（最早的确定性代理）。
func (e *Engine) rootCause(id string, failed map[string]error) string {
	best := ""
	seen := map[string]bool{id: true}
	stack := []string{id}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, p := range e.g.Preds(cur) {
			if seen[p] {
				continue
			}
			seen[p] = true
			if failed[p] != nil {
				if best == "" || p < best {
					best = p
				}
				continue
			}
			stack = append(stack, p)
		}
	}
	return best
}

// insert 有序插入，保持就绪队列按 ID 升序。
func insert(s []string, id string) []string {
	i := sort.SearchStrings(s, id)
	return append(s[:i], append([]string{id}, s[i:]...)...)
}
