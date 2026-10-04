package booking

import (
	"container/heap"
	"sync"

	"ontology/credit"
	"ontology/slotpool"
)

// entry 是一条有效预约（候补递补后也转为 entry）。
type entry struct {
	patient string
	slot    string
	seq     int64
	dead    int64 // start+G，爽约落地时刻
	channel slotpool.Channel
	checked bool
	heapIdx int // 到期堆下标；不在堆中为 -1
}

type waiter struct{ patient string }

type slotState struct {
	active   map[string]*entry
	wait     []waiter
	dead     int64 // start+G
	deadSlot string
	pending  int           // 未签到有效预约数
	waitItem *waitHeapItem // waitDead 堆中的项
}

// dueHeap 为按 (dead, seq) 升序的到期预约索引最小堆。
type dueHeap struct{ list []*entry }

func (h dueHeap) Len() int { return len(h.list) }
func (h dueHeap) Less(i, j int) bool {
	if h.list[i].dead != h.list[j].dead {
		return h.list[i].dead < h.list[j].dead
	}
	return h.list[i].seq < h.list[j].seq
}
func (h dueHeap) Swap(i, j int) {
	h.list[i], h.list[j] = h.list[j], h.list[i]
	h.list[i].heapIdx = i
	h.list[j].heapIdx = j
}
func (h *dueHeap) Push(x any) {
	e := x.(*entry)
	e.heapIdx = len(h.list)
	h.list = append(h.list, e)
}
func (h *dueHeap) Pop() any {
	n := len(h.list)
	e := h.list[n-1]
	e.heapIdx = -1
	h.list = h.list[:n-1]
	return e
}

func heapPush(h *dueHeap, e *entry) { heap.Push(h, e) }

// popRemove 索引删除堆中预约（无惰性墓碑）。
func popRemove(h *dueHeap, idx int) {
	if idx < 0 || idx >= h.Len() {
		return
	}
	heap.Remove(h, idx)
}

// waitHeapItem 记录带候补槽的死期；pending=0 且到死期时候补作废。
type waitHeapItem struct {
	slot    string
	dead    int64
	pending int
	idx     int
}

type waitHeap struct{ list []*waitHeapItem }

func (h waitHeap) Len() int { return len(h.list) }
func (h waitHeap) Less(i, j int) bool {
	if h.list[i].dead != h.list[j].dead {
		return h.list[i].dead < h.list[j].dead
	}
	return h.list[i].slot < h.list[j].slot
}
func (h waitHeap) Swap(i, j int) {
	h.list[i], h.list[j] = h.list[j], h.list[i]
	h.list[i].idx = i
	h.list[j].idx = j
}
func (h *waitHeap) Push(x any) {
	it := x.(*waitHeapItem)
	it.idx = len(h.list)
	h.list = append(h.list, it)
}
func (h *waitHeap) Pop() any {
	n := len(h.list)
	it := h.list[n-1]
	h.list = h.list[:n-1]
	return it
}
func (h *waitHeap) push(it *waitHeapItem) { heap.Push(h, it) }
func (h *waitHeap) remove(it *waitHeapItem) {
	if it == nil || it.idx < 0 || it.idx >= len(h.list) || h.list[it.idx] != it {
		return
	}
	heap.Remove(h, it.idx)
}

// System 是门诊号源系统。
type System struct {
	r, e, g, c int64
	k, w       int64

	mu     sync.Mutex
	maxNow int64
	seq    int64

	pool *slotpool.Pool
	cred *credit.Store

	slots    map[string]*slotState
	due      *dueHeap
	waitDead *waitHeap // “有候补且 pending=0”槽的死期堆，到死期作废候补

	landTaken   int // 最近一次落地从到期堆取出的预约数
	landProbed  int // 最近一次落地额外探测（未到期堆顶）次数
	landApplied int // 最近一次落地实际爽约数
}
