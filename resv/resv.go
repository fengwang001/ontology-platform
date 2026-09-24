// Package resv 实现 A-Res 蓄水池：键计算、排名全序、小顶堆替换。
package resv

import (
	"container/heap"
	"math"
	"sort"

	"ontology/rnd"
)

// Item 是池中条目：ID、权重、键与到达序号（键相等时先到者排名高）。
type Item struct {
	ID  string
	W   float64
	Key float64
	seq int
}

// heapImpl 是以排名最低者为堆顶的小顶堆；Less 内计数每次比较。
type heapImpl struct {
	items  []Item
	visits *int
}

func (h heapImpl) Len() int { return len(h.items) }

// Less 报告 i 的排名是否低于 j：键小者低；键相等时后到者低。
func (h heapImpl) Less(i, j int) bool {
	*h.visits++
	a, b := h.items[i], h.items[j]
	return a.Key < b.Key || (a.Key == b.Key && a.seq > b.seq)
}

func (h heapImpl) Swap(i, j int) { h.items[i], h.items[j] = h.items[j], h.items[i] }
func (h *heapImpl) Push(x any)   { h.items = append(h.items, x.(Item)) }
func (h *heapImpl) Pop() (v any) {
	v = h.items[len(h.items)-1]
	h.items = h.items[:len(h.items)-1]
	return
}

// Pool 是容量为 k 的蓄水池，非并发安全（由上层串行化）。
type Pool struct {
	k      int
	h      heapImpl
	seq    int
	visits int // 最近一次 Consider 访问过的池内条目比较次数，非导出
}

// NewPool 创建容量为 k 的蓄水池，k 必须为正（由调用方保证）。
func NewPool(k int) *Pool {
	p := &Pool{k: k}
	p.h.visits = &p.visits
	return p
}

// Consider 从 src 取一个 u，计算键 key=u^(1/W)，按排名全序决定插入/替换/未入选。
// 返回是否进入池中；随机源用尽时返回错误且池不变。
func (p *Pool) Consider(src *rnd.Source, id string, w float64) (bool, error) {
	u, err := src.Next()
	if err != nil {
		return false, err
	}
	p.visits = 0
	p.seq++
	it := Item{ID: id, W: w, Key: math.Pow(u, 1/w), seq: p.seq}
	if p.h.Len() < p.k {
		heap.Push(&p.h, it)
		return true, nil
	}
	p.visits++                      // 与堆顶（池中排名最低者）比较
	if it.Key <= p.h.items[0].Key { // 键相等时新元素永不替换
		return false, nil
	}
	p.h.items[0] = it
	heap.Fix(&p.h, 0)
	return true, nil
}

// Len 返回池中条目数。
func (p *Pool) Len() int { return p.h.Len() }

// Items 按排名从高到低返回池中条目。
func (p *Pool) Items() []Item {
	out := append([]Item(nil), p.h.items...)
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		return a.Key > b.Key || (a.Key == b.Key && a.seq < b.seq)
	})
	return out
}
