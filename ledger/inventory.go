package ledger

import "container/heap"

// batch 是一个入库批次。账面余量 qty 保留到被销毁为止；
// 过期（expiry <= now）后不可发出、不计入可用库存，但账面保留。
type batch struct {
	id      string
	qty     int
	expiry  int64
	seq     int // 入库先后序号，效期相同时按此排序
	expired bool
	index   int // 在 activeHeap 中的下标；-1 表示不在堆中
}

// batchHeap 是按 (expiry, seq) 排序的最小堆，只存放
// 未过期且账面余量大于 0 的批次。堆键不含 qty，
// 因此扣减/退回数量不需要调整堆结构。
type batchHeap []*batch

func (h batchHeap) Len() int { return len(h) }

func (h batchHeap) Less(i, j int) bool {
	if h[i].expiry != h[j].expiry {
		return h[i].expiry < h[j].expiry
	}
	return h[i].seq < h[j].seq
}

func (h batchHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].index = i
	h[j].index = j
}

func (h *batchHeap) Push(x any) {
	b := x.(*batch)
	b.index = len(*h)
	*h = append(*h, b)
}

func (h *batchHeap) Pop() any {
	old := *h
	n := len(old)
	b := old[n-1]
	old[n-1] = nil
	b.index = -1
	*h = old[:n-1]
	return b
}

// drugStock 记录一种药品的全部批次与流水计数。
// 不变式：bookTotal == totalIn - totalDestroyed - totalIssued + totalReturned。
type drugStock struct {
	batches    map[string]*batch
	active     batchHeap // 未过期且 qty>0 的批次
	expiredQty int       // 已过期批次的账面余量合计
	bookTotal  int
	nextSeq    int
	stats      *Stats

	totalIn        int
	totalDestroyed int
	totalIssued    int
	totalReturned  int
}

func newDrugStock(stats *Stats) *drugStock {
	return &drugStock{batches: make(map[string]*batch), stats: stats}
}

// migrate 把在 now 时刻已经过期的批次从可发堆中迁出。
// 由于 now 单调不减，每个批次最多被迁移一次，总开销摊还 O(log n)。
func (d *drugStock) migrate(now int64) {
	for len(d.active) > 0 && d.active[0].expiry <= now {
		b := heap.Pop(&d.active).(*batch)
		d.stats.BatchHeapOps++
		b.expired = true
		d.expiredQty += b.qty
	}
}

// peekExpiredQty 非变异地计算可发堆中在 now 时刻已过期的账面量。
// 利用最小堆性质只向下访问 expiry <= now 的节点，
// 开销 O(已过期节点数)，与历史批次总数无关；被拒绝的操作因此无需迁移。
func (d *drugStock) peekExpiredQty(now int64) int {
	return peekExpired(d.active, 0, now)
}

func peekExpired(h batchHeap, i int, now int64) int {
	if i >= len(h) || h[i].expiry > now {
		return 0
	}
	return h[i].qty + peekExpired(h, 2*i+1, now) + peekExpired(h, 2*i+2, now)
}

// availableAt 非变异地返回 now 时刻的可发库存。
func (d *drugStock) availableAt(now int64) int {
	return d.bookTotal - d.expiredQty - d.peekExpiredQty(now)
}

// addInbound 登记一个新批次；按入库时刻的 now 判定是否已过期。
func (d *drugStock) addInbound(id string, qty int, expiry int64, now int64) *batch {
	b := &batch{id: id, qty: qty, expiry: expiry, seq: d.nextSeq, index: -1}
	d.nextSeq++
	if expiry <= now {
		b.expired = true
		d.expiredQty += qty
	} else {
		heap.Push(&d.active, b)
		d.stats.BatchHeapOps++
	}
	d.batches[id] = b
	d.bookTotal += qty
	d.totalIn += qty
	return b
}

// returnTo 把退回量退回原批次账面（即使该批次已过期）。
func (d *drugStock) returnTo(b *batch, qty int) {
	b.qty += qty
	d.bookTotal += qty
	d.totalReturned += qty
	if b.expired {
		d.expiredQty += qty
	} else if b.index < 0 {
		heap.Push(&d.active, b)
		d.stats.BatchHeapOps++
	}
}

// popTop 弹出堆顶批次（领用取空时调用）。
func (d *drugStock) popTop() *batch {
	d.stats.BatchHeapOps++
	return heap.Pop(&d.active).(*batch)
}

// destroy 销毁批次账面余量；未过期批次也允许销毁。
func (d *drugStock) destroy(b *batch, qty int) {
	b.qty -= qty
	d.bookTotal -= qty
	d.totalDestroyed += qty
	if b.expired {
		d.expiredQty -= qty
	} else if b.qty == 0 && b.index >= 0 {
		heap.Remove(&d.active, b.index)
		d.stats.BatchHeapOps++
	}
}
