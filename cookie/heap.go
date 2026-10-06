package cookie

import "time"

// 本文件实现三类惰性堆：堆项携带条目代际号 gen，
// 条目被访问/覆盖时 gen 递增并压入新堆项，旧堆项弹出时发现 gen
// 不一致即丢弃。由此淘汰选择的代价为摊还 O(log n)，
// 不随站点条目数线性增长。

// lruItem 按 (最近访问, 创建时刻, 序号) 升序，堆顶为最该淘汰者。
type lruItem struct {
	k        key
	lastSeen time.Time
	created  time.Time
	seq      uint64
	gen      uint64
}

type lruHeap []lruItem

func (h lruHeap) Len() int { return len(h) }

func (h lruHeap) Less(i, j int) bool {
	a, b := h[i], h[j]
	if !a.lastSeen.Equal(b.lastSeen) {
		return a.lastSeen.Before(b.lastSeen)
	}
	if !a.created.Equal(b.created) {
		return a.created.Before(b.created)
	}
	return a.seq < b.seq
}

func (h lruHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }

func (h *lruHeap) Push(x any) { *h = append(*h, x.(lruItem)) }

func (h *lruHeap) Pop() any {
	old := *h
	n := len(old)
	it := old[n-1]
	*h = old[:n-1]
	return it
}

// expiryItem 按过期时刻升序，堆顶为最早过期者。
type expiryItem struct {
	k       key
	expires time.Time
	seq     uint64
	gen     uint64
}

type expiryHeap []expiryItem

func (h expiryHeap) Len() int { return len(h) }

func (h expiryHeap) Less(i, j int) bool {
	a, b := h[i], h[j]
	if !a.expires.Equal(b.expires) {
		return a.expires.Before(b.expires)
	}
	return a.seq < b.seq
}

func (h expiryHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }

func (h *expiryHeap) Push(x any) { *h = append(*h, x.(expiryItem)) }

func (h *expiryHeap) Pop() any {
	old := *h
	n := len(old)
	it := old[n-1]
	*h = old[:n-1]
	return it
}

// siteItem 按条目数降序、并列按站点名字序，用于全局淘汰时选出
// 条目数最多的站点。gen 与 siteIndex.gen 对应，计数变化即失效。
type siteItem struct {
	name  string
	count int
	gen   uint64
}

type siteHeap []siteItem

func (h siteHeap) Len() int { return len(h) }

func (h siteHeap) Less(i, j int) bool {
	if h[i].count != h[j].count {
		return h[i].count > h[j].count
	}
	return h[i].name < h[j].name
}

func (h siteHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }

func (h *siteHeap) Push(x any) { *h = append(*h, x.(siteItem)) }

func (h *siteHeap) Pop() any {
	old := *h
	n := len(old)
	it := old[n-1]
	*h = old[:n-1]
	return it
}
