// Package wfq 实现虚拟完成时刻加权公平调度器。
package wfq

import (
	"container/heap"
	"errors"

	"ontology/flow"
)

var (
	// ErrEmptyWeights 表示 weights 为空。
	ErrEmptyWeights = errors.New("wfq: empty weights")
	// ErrBadWeight 表示存在权重 <= 0。
	ErrBadWeight = errors.New("wfq: weight must be >= 1")
	// ErrFlowRange 表示 flow 下标越界。
	ErrFlowRange = errors.New("wfq: flow index out of range")
	// ErrBadSize 表示包大小 <= 0。
	ErrBadSize = errors.New("wfq: size must be >= 1")
	// ErrEmpty 表示所有流都空时出队。
	ErrEmpty = errors.New("wfq: all flows empty")
)

// idxHeap 是流下标的最小堆，排序键为 (队头包完成时刻, 流下标)。
type idxHeap struct {
	s  *Scheduler
	ix []int
}

func (h *idxHeap) Len() int { return len(h.ix) }
func (h *idxHeap) Less(i, j int) bool {
	a, _ := h.s.flows[h.ix[i]].Head()
	b, _ := h.s.flows[h.ix[j]].Head()
	if a != b {
		return a < b
	}
	return h.ix[i] < h.ix[j]
}
func (h *idxHeap) Swap(i, j int) { h.ix[i], h.ix[j] = h.ix[j], h.ix[i] }
func (h *idxHeap) Push(x any)    { h.ix = append(h.ix, x.(int)) }
func (h *idxHeap) Pop() any {
	old := h.ix
	x := old[len(old)-1]
	h.ix = old[:len(old)-1]
	return x
}

// Scheduler 是 WFQ 调度器：流的集合 + 虚拟时钟 + 定位最小完成时刻的最小堆。
// checked 记录最近一次 Dequeue 为定位最小完成时刻而检查的流个数（堆只需看堆顶 1 条）。
type Scheduler struct {
	flows   []*flow.Flow
	v       int
	h       *idxHeap
	checked int
}

// New 校验 weights 并创建调度器；校验失败不改变任何状态。
func New(weights []int) (*Scheduler, error) {
	if len(weights) == 0 {
		return nil, ErrEmptyWeights
	}
	for _, w := range weights {
		if w < 1 {
			return nil, ErrBadWeight
		}
	}
	s := &Scheduler{}
	for _, w := range weights {
		s.flows = append(s.flows, flow.New(w))
	}
	s.h = &idxHeap{s: s}
	return s, nil
}

// Submit 提交一个包到指定流；先校验后改状态，被拒时不留痕。
func (s *Scheduler) Submit(flowIdx, size int) error {
	if flowIdx < 0 || flowIdx >= len(s.flows) {
		return ErrFlowRange
	}
	if size < 1 {
		return ErrBadSize
	}
	f := s.flows[flowIdx]
	wasEmpty := f.Len() == 0
	f.Submit(size, s.v)
	if wasEmpty { // 流由空转非空，队头出现，入堆
		heap.Push(s.h, flowIdx)
	}
	return nil
}

// Dequeue 出队队头完成时刻最小的流（并列取下标最小），把 V 推进为该包完成时刻。
func (s *Scheduler) Dequeue() (int, error) {
	if s.h.Len() == 0 {
		return 0, ErrEmpty
	}
	s.checked = 1 // 最小堆：定位最小完成时刻只需检查堆顶这 1 条流
	idx := heap.Pop(s.h).(int)
	f := s.flows[idx]
	s.v = f.Pop()
	if f.Len() > 0 { // 仍有队头包，以新队头完成时刻回堆
		heap.Push(s.h, idx)
	}
	return idx, nil
}

// VirtualTime 返回当前虚拟时钟 V。
func (s *Scheduler) VirtualTime() int { return s.v }

// Finish 返回指定流的虚拟完成时刻 F。
func (s *Scheduler) Finish(flowIdx int) int { return s.flows[flowIdx].Finish() }

// QueueLen 返回指定流的队列长度。
func (s *Scheduler) QueueLen(flowIdx int) int { return s.flows[flowIdx].Len() }

// NumFlows 返回流条数。
func (s *Scheduler) NumFlows() int { return len(s.flows) }

// HeadsAscending 报告每条流内包完成时刻是否升序（自检用）。
func (s *Scheduler) HeadsAscending() bool {
	for _, f := range s.flows {
		if !f.Ascending() {
			return false
		}
	}
	return true
}
