package locker

import (
	"container/heap"
	"strconv"
)

// Code 取件码。实现为 1 起始的十进制序号，零值表示无码。
type Code int

func (c Code) String() string { return strconv.Itoa(int(c)) }

// codePool 管理取件码的分配与冷却。
//
// 不变量：任一时刻 active 集合中的码互不相同；码失效后进入冷却，
// 自失效时刻起满 cooldown 才可再分配（恰好满即可）。
// 取可用码时取序号最小者，保证相同操作序列重放得到相同取件码序列。
type codePool struct {
	cooldown  int64
	available codeMinHeap     // 当前空闲、可立即分配的码
	cooling   codeCoolingHeap // (availableAt, code) 的最小堆
	active    map[Code]bool
}

func newCodePool(count int, cooldown int64) *codePool {
	p := &codePool{
		cooldown:  cooldown,
		available: make(codeMinHeap, 0, count),
		cooling:   make(codeCoolingHeap, 0),
		active:    make(map[Code]bool, count),
	}
	for i := 1; i <= count; i++ {
		p.available = append(p.available, Code(i))
	}
	heap.Init(&p.available)
	return p
}

// allocate 返回 now 时刻可分配的序号最小码；无可用码时 ok=false。
func (p *codePool) allocate(now int64) (Code, bool) {
	// 到期时间 <= now 的冷却码回到空闲集合（恰好满冷却即可再用）。
	for len(p.cooling) > 0 && p.cooling[0].at <= now {
		heap.Push(&p.available, heap.Pop(&p.cooling).(coolingEntry).code)
	}
	if len(p.available) == 0 {
		return 0, false
	}
	c := heap.Pop(&p.available).(Code)
	p.active[c] = true
	return c, true
}

// release 使码在 now 时刻失效并进入冷却。
func (p *codePool) release(c Code, now int64) {
	if !p.active[c] {
		return
	}
	delete(p.active, c)
	heap.Push(&p.cooling, coolingEntry{at: now + p.cooldown, code: c})
}

type coolingEntry struct {
	at   int64
	code Code
}

// codeMinHeap 取序号最小的空闲码，保证重放确定性。
type codeMinHeap []Code

func (h codeMinHeap) Len() int           { return len(h) }
func (h codeMinHeap) Less(i, j int) bool { return h[i] < h[j] }
func (h codeMinHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *codeMinHeap) Push(x any)        { *h = append(*h, x.(Code)) }
func (h *codeMinHeap) Pop() any {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[:n-1]
	return x
}

// codeCoolingHeap 先按可再用时刻、再按码序号排序。
type codeCoolingHeap []coolingEntry

func (h codeCoolingHeap) Len() int { return len(h) }
func (h codeCoolingHeap) Less(i, j int) bool {
	if h[i].at != h[j].at {
		return h[i].at < h[j].at
	}
	return h[i].code < h[j].code
}
func (h codeCoolingHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *codeCoolingHeap) Push(x any)   { *h = append(*h, x.(coolingEntry)) }
func (h *codeCoolingHeap) Pop() any {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[:n-1]
	return x
}
