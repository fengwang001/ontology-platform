package scheduler

// 内部状态：区间、批与两个惰性失效堆。
//
// 计数器 holdProbes 只统计“取最小值”时查看（peek/pop）的堆项；
// push 内部的 sift 比较不计入，因此探测数与堆的总规模无关。

import "container/heap"

type batch struct {
	id       int64
	interval int64
	from     int64
	to       int64
	acked    bool
}

type interval struct {
	id      int64
	from    int64
	to      int64
	next    int64
	pending int   // 未确认批数
	ver     int64 // 每次 hold 改变或完成时 +1，用于惰性失效全局堆项
	done    bool

	// 未确认批按起点的最小堆；堆中可能残留已确认批，惰性丢弃。
	pendHeap batchHeap
}

// hold 返回区间当前保持值：next 与最小未确认批起点的较小者。
// 调用方须保证区间未完成。
func (iv *interval) hold() int64 {
	if iv.pending > 0 {
		return iv.pendHeap[0] // 仅在堆顶有效时调用；调用方先 cleanup
	}
	return iv.next
}

// batchHeap 按 batch.from 升序（同值按 id 升序）存批号。
type batchHeap []int64

// gItem 是全局 hold 堆项；ver 与区间当前版本不一致即失效。
type gItem struct {
	id   int64
	hold int64
	ver  int64
}

type globalHeap []gItem

func (h globalHeap) Len() int { return len(h) }
func (h globalHeap) Less(i, j int) bool {
	if h[i].hold != h[j].hold {
		return h[i].hold < h[j].hold
	}
	return h[i].id < h[j].id
}
func (h globalHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *globalHeap) Push(x any)   { *h = append(*h, x.(gItem)) }
func (h *globalHeap) Pop() any {
	old := *h
	n := len(old)
	it := old[n-1]
	*h = old[:n-1]
	return it
}

var _ heap.Interface = (*globalHeap)(nil)
