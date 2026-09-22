// Package merge 用 container/heap 做 K 路归并，输出全局有序序列，
// 并用非导出计数器记录键比较次数。
package merge

import (
	"container/heap"

	"ontology/record"
)

// Source 是一路已排序输入。
type Source interface {
	Next() (record.Record, bool)
}

// item 是堆元素：一条记录 + 来源下标。
type item struct {
	rec record.Record
	src int
}

// recHeap 按 record.Less（Key, Seq）组织的堆；less 回调计入比较次数。
type recHeap struct {
	items    []item
	compares *int64
}

func (h *recHeap) Len() int { return len(h.items) }

func (h *recHeap) Less(i, j int) bool {
	*h.compares++
	return record.Less(h.items[i].rec, h.items[j].rec)
}

func (h *recHeap) Swap(i, j int) { h.items[i], h.items[j] = h.items[j], h.items[i] }

func (h *recHeap) Push(x any) { h.items = append(h.items, x.(item)) }

func (h *recHeap) Pop() any {
	old := h.items
	n := len(old)
	it := old[n-1]
	h.items = old[:n-1]
	return it
}

// Merger 归并多路有序输入。compares 为非导出计数器，经 Compares() 读出。
type Merger struct {
	compares int64
}

// NewMerger 构造归并器。
func NewMerger() *Merger { return &Merger{} }

// Compares 返回累计键比较次数。
func (m *Merger) Compares() int64 { return m.compares }

// Merge 把 sources 归并成全局有序序列，逐条交给 emit。
// 每条记录一次 Push 一次 Pop，各约 log2(K) 次比较，
// 总比较次数约 2*N*log2(K)，远低于 Bound 给出的 4*N*ceil(log2(K+1))。
func (m *Merger) Merge(sources []Source, emit func(record.Record) error) error {
	h := &recHeap{compares: &m.compares}
	for i, src := range sources {
		if rec, ok := src.Next(); ok {
			h.items = append(h.items, item{rec: rec, src: i})
		}
	}
	heap.Init(h)
	for h.Len() > 0 {
		top := heap.Pop(h).(item)
		if err := emit(top.rec); err != nil {
			return err
		}
		if rec, ok := sources[top.src].Next(); ok {
			heap.Push(h, item{rec: rec, src: top.src})
		}
	}
	return nil
}

// Bound 返回比较次数上界 4*N*ceil(log2(K+1))。
func Bound(n, k int64) int64 {
	if n <= 0 || k <= 0 {
		return 0
	}
	// ceil(log2(k+1))
	v := uint64(k + 1)
	log := 0
	for (uint64(1) << log) < v {
		log++
	}
	return 4 * n * int64(log)
}
