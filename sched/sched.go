// Package sched 按依赖关系并行调度任务，并在失败时传播与取消。
package sched

import (
	"context"
	"sort"

	"ontology/exec"
	"ontology/fail"
	"ontology/graph"
	"ontology/report"
)

// Scheduler 在单 goroutine 事件循环中驱动全部状态迁移，无锁。
type Scheduler struct {
	g     *graph.Graph
	funcs map[string]exec.Func
	limit int
	mode  fail.Mode

	maxConcurrent int
	readyChecks   int
}

// New 构造调度器；limit 小于 1 时按 1 处理（退化为串行）。
func New(g *graph.Graph, funcs map[string]exec.Func, limit int, mode fail.Mode) *Scheduler {
	if limit < 1 {
		limit = 1
	}
	return &Scheduler{g: g, funcs: funcs, limit: limit, mode: mode}
}

// MaxConcurrent 返回历史最大同时运行任务数。
func (s *Scheduler) MaxConcurrent() int { return s.maxConcurrent }

// ReadyChecks 返回就绪判定总次数（初始扫描 + 边松弛 + 传播遍历）。
func (s *Scheduler) ReadyChecks() int { return s.readyChecks }

type outcome struct {
	id  string
	res exec.Result
}

// Run 执行整张图并返回确定性报告；图有环时不执行任何任务并返回错误。
func (s *Scheduler) Run(ctx context.Context) (*report.Report, error) {
	if _, err := s.g.Layers(); err != nil {
		return nil, err
	}
	ids := s.g.Tasks()
	tr := fail.NewTracker(ids)
	indeg := make(map[string]int, len(ids))
	var ready []string
	for _, id := range ids {
		s.readyChecks++
		if indeg[id] = len(s.g.Deps(id)); indeg[id] == 0 {
			ready = insertSorted(ready, id)
		}
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	results := make(chan outcome, s.limit)
	running := map[string]bool{}
	canceled := map[string]bool{}
	pending := len(ids)
	firstFailure := ""
	for pending > 0 {
		for firstFailure == "" || s.mode == fail.BestEffort {
			if len(running) >= s.limit || len(ready) == 0 {
				break
			}
			var id string
			id, ready = ready[0], ready[1:]
			if tr.Status(id) != fail.Pending {
				continue
			}
			tr.Start(id)
			running[id] = true
			go func() { results <- outcome{id, exec.Run(ctx, s.funcs[id])} }()
		}
		if len(running) > s.maxConcurrent {
			s.maxConcurrent = len(running)
		}
		if len(running) == 0 {
			break
		}
		o := <-results
		delete(running, o.id)
		pending--
		if canceled[o.id] {
			tr.Cancel(o.id) // 取消后写回的结果被丢弃，状态定格为 Canceled
			continue
		}
		if o.res.Err == nil {
			tr.Succeed(o.id)
			for _, dep := range s.g.Dependents(o.id) {
				s.readyChecks++
				if indeg[dep]--; indeg[dep] == 0 && tr.Status(dep) == fail.Pending {
					ready = insertSorted(ready, dep)
				}
			}
			continue
		}
		tr.Fail(o.id, o.res.Err)
		edges, skipped := tr.PropagateSkip(s.g, o.id, o.id)
		s.readyChecks += edges
		pending -= skipped
		if firstFailure != "" {
			continue
		}
		firstFailure = o.id
		if s.mode == fail.FailFast {
			cancel()
			for id := range running {
				canceled[id] = true
			}
			pending -= tr.SkipAllPending(firstFailure)
		}
	}
	entries := make([]report.Entry, 0, len(ids))
	for _, id := range ids {
		entries = append(entries, report.Entry{
			ID: id, Status: tr.Status(id), Cause: tr.Cause(id), Err: tr.Err(id),
		})
	}
	return report.New(entries), nil
}

// insertSorted 保持就绪队列按 ID 升序，同时就绪时取最小 ID。
func insertSorted(s []string, id string) []string {
	i := sort.SearchStrings(s, id)
	s = append(s, "")
	copy(s[i+1:], s[i:])
	s[i] = id
	return s
}
