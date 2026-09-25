// Package sched 按依赖并行调度任务图：并发度上限、就绪堆、失败传播与确定性报告。
package sched

import (
	"container/heap"
	"context"
	"errors"
	"fmt"

	"ontology/exec"
	"ontology/fail"
	"ontology/graph"
	"ontology/report"
)

// Scheduler 以并发度上限调度一张图；计数器非导出，经访问器读取。
type Scheduler struct {
	limit      int
	maxRunning int // 历史最大同时运行任务数
	decide     int // 就绪判定总次数
}

// New 创建调度器；limit<1 时按 1 处理（退化为串行）。
func New(limit int) *Scheduler {
	if limit < 1 {
		limit = 1
	}
	return &Scheduler{limit: limit}
}

// Peak 返回历史最大同时运行任务数。
func (s *Scheduler) Peak() int { return s.maxRunning }

// Decisions 返回就绪判定总次数（初始入度扫描 + 逐边松弛）。
func (s *Scheduler) Decisions() int { return s.decide }

type slot struct {
	status  fail.Status
	started bool
	remain  int
	origin  graph.ID // 最小 ID 的失败祖先（失败任务均为根失败）
	err     error
}

// Run 执行整张图并返回确定性报告；图有环或缺少任务函数时返回错误。
func (s *Scheduler) Run(ctx context.Context, g *graph.Graph, fns map[graph.ID]exec.Func, mode fail.Mode) (*report.Report, error) {
	if err := g.Check(); err != nil {
		return nil, err
	}
	rep := report.New()
	nodes := g.Nodes()
	for _, id := range nodes {
		if fns[id] == nil {
			return nil, fmt.Errorf("missing task func: %s", id)
		}
	}
	slots := map[graph.ID]*slot{}
	ready := &idHeap{}
	pending := len(nodes)
	fastFailed := false
	running := 0
	results := make(chan outcome, s.limit)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var finish func(id graph.ID)
	resolve := func(id graph.ID) {
		sl := slots[id]
		best := graph.ID("")
		for _, p := range g.Pred(id) {
			cand := slots[p].origin
			if slots[p].status == fail.Failed {
				cand = p
			}
			if cand != "" && (best == "" || cand < best) {
				best = cand
			}
		}
		switch {
		case best != "":
			sl.status, sl.origin, sl.err = fail.Skipped, best, fail.ErrSkipped
		case fastFailed:
			sl.status, sl.err = fail.Canceled, fail.ErrCanceled
		default:
			heap.Push(ready, id)
			return
		}
		pending--
		finish(id)
	}
	finish = func(id graph.ID) {
		for _, succ := range g.Succ(id) {
			s.decide++
			slots[succ].remain--
			if slots[succ].remain == 0 {
				resolve(succ)
			}
		}
	}

	for _, id := range nodes {
		s.decide++
		slots[id] = &slot{status: fail.Pending, remain: len(g.Pred(id))}
		if slots[id].remain == 0 {
			heap.Push(ready, id)
		}
	}
	for pending > 0 {
		for !fastFailed && running < s.limit && ready.Len() > 0 {
			id := heap.Pop(ready).(graph.ID)
			slots[id].status, slots[id].started = fail.Running, true
			running++
			if running > s.maxRunning {
				s.maxRunning = running
			}
			go func(id graph.ID) {
				results <- outcome{id, exec.Run(ctx, fns[id]).Err}
			}(id)
		}
		if running == 0 {
			break
		}
		o := <-results
		running--
		sl := slots[o.id]
		switch {
		case o.err == nil && fastFailed:
			sl.status, sl.err = fail.Canceled, fail.ErrCanceled
		case o.err == nil:
			sl.status = fail.Success
		case fastFailed && errors.Is(o.err, context.Canceled):
			sl.status, sl.err = fail.Canceled, fail.ErrCanceled
		default:
			sl.status, sl.err = fail.Failed, o.err
			if mode == fail.FastFail && !fastFailed {
				fastFailed = true
				cancel()
				for ready.Len() > 0 {
					id := heap.Pop(ready).(graph.ID)
					slots[id].status, slots[id].err = fail.Canceled, fail.ErrCanceled
					pending--
					finish(id)
				}
			}
		}
		pending--
		finish(o.id)
	}
	for _, id := range nodes {
		sl := slots[id]
		e := report.Entry{Status: sl.status, Started: sl.started, Err: sl.err}
		if sl.status == fail.Failed {
			e.Origin = id
		} else {
			e.Origin = sl.origin
		}
		rep.Set(id, e)
	}
	return rep, nil
}

type outcome struct {
	id  graph.ID
	err error
}

// idHeap 是按 ID 升序的就绪最小堆，保证同时就绪时按字典序派发。
type idHeap []graph.ID

func (h idHeap) Len() int           { return len(h) }
func (h idHeap) Less(i, j int) bool { return h[i] < h[j] }
func (h idHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *idHeap) Push(x any)        { *h = append(*h, x.(graph.ID)) }
func (h *idHeap) Pop() any {
	old := *h
	it := old[len(old)-1]
	*h = old[:len(old)-1]
	return it
}
