// Package buddy 实现按大小分档的空闲链表与伙伴分裂/合并。
package buddy

import (
	"container/heap"
	"sort"
)

// Block 是一个空闲块：起始偏移 Off，大小 Size（2 的幂）。
type Block struct {
	Off  int
	Size int
}

// minHeap 是偏移最小堆，保证每次取到该档最小偏移（确定性分配）。
type minHeap []int

func (h minHeap) Len() int           { return len(h) }
func (h minHeap) Less(i, j int) bool { return h[i] < h[j] }
func (h minHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *minHeap) Push(x any)        { *h = append(*h, x.(int)) }
func (h *minHeap) Pop() any {
	o := *h
	old := o[len(o)-1]
	*h = o[:len(o)-1]
	return old
}

// FreeLists 按 2 的幂阶（order，块大小 1<<order）分档管理空闲块。
// 每档一个最小堆负责取最小偏移，一个成员集负责 O(1) 伙伴查询；
// 合并时只做成员集删除，堆中残项在取用时惰性清除。
type FreeLists struct {
	maxOrder int
	heaps    []minHeap
	member   []map[int]bool
}

// New 建立管理 [0, 1<<maxOrder) 的空闲链表，初始只有整池一块。
func New(maxOrder int) *FreeLists {
	f := &FreeLists{
		maxOrder: maxOrder,
		heaps:    make([]minHeap, maxOrder+1),
		member:   make([]map[int]bool, maxOrder+1),
	}
	for i := range f.member {
		f.member[i] = map[int]bool{}
	}
	f.put(0, maxOrder)
	return f
}

func (f *FreeLists) put(off, order int) {
	heap.Push(&f.heaps[order], off)
	f.member[order][off] = true
}

// top 清除堆顶残项后返回该档最小偏移；checks 计入每次成员集查验。
func (f *FreeLists) top(order int) (off, checks int, ok bool) {
	h := &f.heaps[order]
	for h.Len() > 0 {
		checks++
		if f.member[order][(*h)[0]] {
			return (*h)[0], checks, true
		}
		heap.Pop(h) // 残项：已被合并拿走
	}
	return 0, checks, false
}

// Take 取走一个 order 阶空闲块，必要时从更大的块逐次二等分；
// 返回块偏移与本次检查（访问）的空闲块记录条数。
func (f *FreeLists) Take(order int) (off, checks int, ok bool) {
	for o := order; o <= f.maxOrder; o++ {
		top, c, found := f.top(o)
		checks += c
		if !found {
			continue
		}
		heap.Pop(&f.heaps[o])
		delete(f.member[o], top)
		for o > order { // 二等分，右半块（伙伴）入档
			o--
			f.put(top+(1<<o), o)
		}
		return top, checks, true
	}
	return 0, checks, false
}

// Release 归还 order 阶块，并沿伙伴链级联合并：伙伴当且仅当
// 大小相同且 off XOR 2^order == 伙伴off。返回检查条数。
func (f *FreeLists) Release(off, order int) (checks int) {
	for order < f.maxOrder {
		b := off ^ (1 << order)
		checks++
		if !f.member[order][b] {
			break
		}
		delete(f.member[order], b) // 堆中残项惰性清除
		if b < off {
			off = b
		}
		order++
	}
	f.put(off, order)
	return checks
}

// Blocks 返回全部空闲块，按偏移升序。
func (f *FreeLists) Blocks() []Block {
	var out []Block
	for o, m := range f.member {
		for off := range m {
			out = append(out, Block{Off: off, Size: 1 << o})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Off < out[j].Off })
	return out
}
