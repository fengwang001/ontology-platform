// Package merge 把多份「按 key 升序」的部分结果做多路归并：
// 同 key 跨分区求和，产出按 key 全序的全局结果。依赖 pagg。
package merge

import (
	"container/heap"
	"sync/atomic"

	"ontology/pagg"
)

// lastCompares 记录最近一次 Merge 为找「最小头」比较 key 的次数。
// 非导出，不出现在任何公开接口里；仅本包内部（含包内测试）可见。
var lastCompares atomic.Int64

// cursor 指向某份部分结果中尚未归并的第一个元素。
type cursor struct {
	entries []pagg.Entry
	idx     int
}

// minHeap 是按当前头元素 key 排序的最小堆；Less 里的计数即「找最小头」的比较。
type minHeap struct {
	items    []cursor
	compares *int64
}

func (h *minHeap) Len() int { return len(h.items) }
func (h *minHeap) Less(i, j int) bool {
	*h.compares++
	a, b := h.items[i], h.items[j]
	return a.entries[a.idx].Key < b.entries[b.idx].Key
}
func (h *minHeap) Swap(i, j int) { h.items[i], h.items[j] = h.items[j], h.items[i] }
func (h *minHeap) Push(x any)    { h.items = append(h.items, x.(cursor)) }
func (h *minHeap) Pop() (old any) {
	old = h.items[len(h.items)-1]
	h.items = h.items[:len(h.items)-1]
	return
}

// Merge 对 partials（每份按 key 升序）做多路归并：同 key 求和，
// 返回按 key 升序全序的全局结果，每个 key 恰好出现一次。
// 结果与 partials 的给出顺序（分区完成顺序）无关。
func Merge(partials [][]pagg.Entry) []pagg.Entry {
	var compares int64
	h := &minHeap{compares: &compares}
	for _, part := range partials {
		if len(part) > 0 {
			heap.Push(h, cursor{entries: part})
		}
	}
	var out []pagg.Entry
	for h.Len() > 0 {
		c := heap.Pop(h).(cursor)
		e := c.entries[c.idx]
		if n := len(out); n > 0 && out[n-1].Key == e.Key {
			out[n-1].Total += e.Total // 同 key 跨分区：求和而非覆盖
		} else {
			out = append(out, e)
		}
		if c.idx+1 < len(c.entries) {
			heap.Push(h, cursor{entries: c.entries, idx: c.idx + 1})
		}
	}
	lastCompares.Store(compares)
	return out
}
