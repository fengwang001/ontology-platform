package ontology

import "container/heap"

// schedEntry 是到期堆中的惰性条目：generation 与租约当前代数不符即丢弃。
type schedEntry struct {
	at         int
	leaseID    string
	generation int
	kind       byte
	offerID    string // 逾期事件绑定的要约，被撤回/拒绝/替换后立即失效
	index      int
}

const (
	kindProtection  byte = iota // 保护期起始日（end-A+1）
	kindEnd                     // 固定租期届满（end）
	kindTenantLate              // 租户答复逾期（截止日次日 issue+C 起）
	kindCounterLate             // 房东对反要约答复逾期（counterDay+C 起）
	kindTerminate               // 延续通知生效日（notice+D）
)

type schedHeap []*schedEntry

func (h schedHeap) Len() int           { return len(h) }
func (h schedHeap) Less(i, j int) bool { return h[i].at < h[j].at }
func (h schedHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i]; h[i].index, h[j].index = i, j }
func (h *schedHeap) Push(x any)        { e := x.(*schedEntry); e.index = len(*h); *h = append(*h, e) }
func (h *schedHeap) Pop() any {
	old := *h
	n := len(old)
	e := old[n-1]
	old[n-1] = nil
	*h = old[:n-1]
	return e
}

// scheduler 持有按日序号排列的到期事件。
// advanceTo 弹出的堆顶条目数等于该区间真实到期事件数（加极少量被续签
// 作废的陈旧条目），与租约总数 N 无关，故判定逾期与落入延续均为 O(到期数)。
type scheduler struct {
	h schedHeap
}

func newScheduler() *scheduler {
	sch := &scheduler{}
	heap.Init(&sch.h)
	return sch
}

func (s *scheduler) push(at int, leaseID string, generation int, kind byte, offerID string) {
	heap.Push(&s.h, &schedEntry{
		at: at, leaseID: leaseID, generation: generation, kind: kind, offerID: offerID,
	})
}

func (s *scheduler) pushRaw(e *schedEntry) { heap.Push(&s.h, e) }

// peekPop 临时弹出所有 at<=day 的条目（含陈旧条目），返回原始弹出序列。
// 配合 restore 实现“拒绝操作不留痕”：调用方失败后把条目全部放回。
func (s *scheduler) peekPop(day int) []*schedEntry {
	var out []*schedEntry
	for s.h.Len() > 0 && s.h[0].at <= day {
		e := heap.Pop(&s.h).(*schedEntry)
		out = append(out, e)
	}
	return out
}

// restore 把 peekPop 弹出的条目重新放回堆。
func (s *scheduler) restore(entries []*schedEntry) {
	for _, e := range entries {
		heap.Push(&s.h, e)
	}
}
