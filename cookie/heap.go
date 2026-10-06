package cookie

import "container/heap"

// keyString 生成键的稳定字符串序，用于堆并列打破。
func keyString(k Key) string {
	return k.Name + "\x00" + k.Domain + "\x00" + k.Path + "\x00" + k.Partition
}

// lruHeap 每站点一个：最近访问最早者优先，并列按创建时刻、再按键序。
// compares 累计比较次数，供性能测试验证淘汰选择的对数级开销。
type lruHeap struct {
	items    []*entry
	compares int64
}

func (h *lruHeap) Len() int { return len(h.items) }
func (h *lruHeap) Less(i, j int) bool {
	h.compares++
	a, b := h.items[i], h.items[j]
	if a.lastAccess != b.lastAccess {
		return a.lastAccess < b.lastAccess
	}
	if a.createdAt != b.createdAt {
		return a.createdAt < b.createdAt
	}
	return keyString(a.key) < keyString(b.key)
}
func (h *lruHeap) Swap(i, j int) {
	h.items[i], h.items[j] = h.items[j], h.items[i]
	h.items[i].lruIndex = i
	h.items[j].lruIndex = j
}
func (h *lruHeap) Push(x any) {
	e := x.(*entry)
	e.lruIndex = len(h.items)
	h.items = append(h.items, e)
}
func (h *lruHeap) Pop() any {
	old := h.items
	n := len(old)
	e := old[n-1]
	old[n-1] = nil
	e.lruIndex = -1
	h.items = old[:n-1]
	return e
}

// expiryHeap 每站点一个：过期时刻最早者优先，并列按键序保证可复现。
type expiryHeap []*entry

func (h expiryHeap) Len() int { return len(h) }
func (h expiryHeap) Less(i, j int) bool {
	a, b := h[i], h[j]
	if *a.expires != *b.expires {
		return *a.expires < *b.expires
	}
	return keyString(a.key) < keyString(b.key)
}
func (h expiryHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].expiryIndex = i
	h[j].expiryIndex = j
}
func (h *expiryHeap) Push(x any) {
	e := x.(*entry)
	e.expiryIndex = len(*h)
	*h = append(*h, e)
}
func (h *expiryHeap) Pop() any {
	old := *h
	n := len(old)
	e := old[n-1]
	old[n-1] = nil
	e.expiryIndex = -1
	*h = old[:n-1]
	return e
}

// gblExpiry 是全局过期堆的节点：为保证站点间确定性，再按站点名、键序排列。
type gblExpiry struct {
	e      *entry
	domain string
}

type gblExpiryHeap []gblExpiry

func (h gblExpiryHeap) Len() int { return len(h) }
func (h gblExpiryHeap) Less(i, j int) bool {
	a, b := h[i], h[j]
	ae, be := *a.e.expires, *b.e.expires
	if ae != be {
		return ae < be
	}
	if a.domain != b.domain {
		return a.domain < b.domain
	}
	return keyString(a.e.key) < keyString(b.e.key)
}
func (h gblExpiryHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].e.gblExpIndex = i
	h[j].e.gblExpIndex = j
}
func (h *gblExpiryHeap) Push(x any) {
	node := x.(gblExpiry)
	node.e.gblExpIndex = len(*h)
	*h = append(*h, node)
}
func (h *gblExpiryHeap) Pop() any {
	old := *h
	n := len(old)
	node := old[n-1]
	old[n-1] = gblExpiry{}
	node.e.gblExpIndex = -1
	*h = old[:n-1]
	return node
}

// siteCount 是全局站点计数堆的节点。
type siteCount struct {
	domain string
	count  int
	idx    int
}

// siteCountHeap 条目数最多的站点优先，并列按站点名字序。
type siteCountHeap []*siteCount

func (h siteCountHeap) Len() int { return len(h) }
func (h siteCountHeap) Less(i, j int) bool {
	a, b := h[i], h[j]
	if a.count != b.count {
		return a.count > b.count
	}
	return a.domain < b.domain
}
func (h siteCountHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].idx = j
	h[j].idx = i
}
func (h *siteCountHeap) Push(x any) {
	s := x.(*siteCount)
	s.idx = len(*h)
	*h = append(*h, s)
}
func (h *siteCountHeap) Pop() any {
	old := *h
	n := len(old)
	s := old[n-1]
	old[n-1] = nil
	s.idx = -1
	*h = old[:n-1]
	return s
}

var (
	_ heap.Interface = (*lruHeap)(nil)
	_ heap.Interface = (*expiryHeap)(nil)
	_ heap.Interface = (*gblExpiryHeap)(nil)
	_ heap.Interface = (*siteCountHeap)(nil)
)
