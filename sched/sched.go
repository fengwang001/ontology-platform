// Package sched 只提供调度原语：按 ID 升序的就绪队列、剩余依赖簿记、
// 并发计量器（历史峰值）与就绪判定次数计数。它本身不执行任何任务。
package sched

import "ontology/graph"

// Scheduler 维护 Kahn 簿记；所有方法预期由单一事件循环 goroutine 调用。
type Scheduler struct {
	g         *graph.Graph
	remaining map[string]int // 每个任务尚未完成的依赖数
	ready     heap
	running   int
	limit     int

	// peak 是历史最大同时运行任务数（非导出，由测试白盒读取）。
	peak int
	// decisions 是就绪判定（松弛/入队/取队）总次数（非导出）。
	decisions int
}

func New(g *graph.Graph, concurrency int) *Scheduler {
	s := &Scheduler{
		g:         g,
		remaining: make(map[string]int, len(g.Nodes())),
		limit:     concurrency,
	}
	for _, id := range g.Nodes() {
		s.remaining[id] = g.InDegree(id)
	}
	for _, id := range g.Nodes() {
		if s.remaining[id] == 0 {
			s.push(id)
		}
	}
	return s
}

func (s *Scheduler) push(id string) {
	s.decisions++
	s.ready.push(id)
}

// Next 返回一个可立即启动的任务；没有名额或没有就绪任务时返回 ""。
func (s *Scheduler) Next() string {
	if s.running >= s.limit || s.ready.len() == 0 {
		return ""
	}
	s.decisions++
	id := s.ready.pop()
	s.running++
	if s.running > s.peak {
		s.peak = s.running
	}
	return id
}

// Done 记录 id 已完成（成功），松弛其出边并把新就绪任务入队。
func (s *Scheduler) Done(id string) {
	s.running--
	for _, s2 := range s.g.Successors(id) {
		s.decisions++ // 每条边至多松弛一次
		if s.remaining[s2] > 0 {
			s.remaining[s2]--
			if s.remaining[s2] == 0 {
				s.push(s2)
			}
		}
	}
}

// Discard 用于不再参与调度的任务（失败/跳过/取消）：仅释放运行名额，不松弛边。
func (s *Scheduler) Discard(id string, wasRunning bool) {
	if wasRunning {
		s.running--
	}
}

func (s *Scheduler) Peak() int { return s.peak }

func (s *Scheduler) Decisions() int { return s.decisions }

// 最小堆：就绪任务按 ID 升序取出。
type heap struct{ ids []string }

func (h *heap) push(id string) {
	h.ids = append(h.ids, id)
	i := len(h.ids) - 1
	for i > 0 {
		p := (i - 1) / 2
		if h.ids[p] <= h.ids[i] {
			break
		}
		h.ids[p], h.ids[i] = h.ids[i], h.ids[p]
		i = p
	}
}

func (h *heap) pop() string {
	top := h.ids[0]
	last := len(h.ids) - 1
	h.ids[0] = h.ids[last]
	h.ids = h.ids[:last]
	for i := 0; ; {
		l, r, best := 2*i+1, 2*i+2, i
		if l < len(h.ids) && h.ids[l] < h.ids[best] {
			best = l
		}
		if r < len(h.ids) && h.ids[r] < h.ids[best] {
			best = r
		}
		if best == i {
			break
		}
		h.ids[i], h.ids[best] = h.ids[best], h.ids[i]
		i = best
	}
	return top
}

func (h *heap) len() int { return len(h.ids) }
