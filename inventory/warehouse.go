package inventory

import "container/heap"

// inbound 是一条计划入库记录。确认到货后从 buckets 的 inbounds 中移除
// 并转入现货，因此不会被重复计入。
type inbound struct {
	id        string
	arrivalAt int64
	qty       int64
}

// reservation 是一条预留记录。同一对象同时被以下两处引用：
//   - 所属（仓库，商品）桶的到期最小堆（用于 ATP 的惰性过期与求和）；
//   - 订单号 -> 预留记录 的索引（用于确认出库、释放与明细查询）。
type reservation struct {
	orderID   string
	product   string
	qty       int64
	expireAt  int64
	bucket    *stockBucket
	heapIndex int // 在桶内最小堆中的下标，-1 表示已不在堆中
}

// reservationHeap 是按到期时刻排序的最小堆（到期时刻相同再按订单号、商品，
// 保证堆行为确定性）。堆顶即最早到期的预留。
type reservationHeap []*reservation

func (h reservationHeap) Len() int { return len(h) }

func (h reservationHeap) Less(i, j int) bool {
	a, b := h[i], h[j]
	if a.expireAt != b.expireAt {
		return a.expireAt < b.expireAt
	}
	if a.orderID != b.orderID {
		return a.orderID < b.orderID
	}
	return a.product < b.product
}

func (h reservationHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].heapIndex = i
	h[j].heapIndex = j
}

func (h *reservationHeap) Push(x any) {
	r := x.(*reservation)
	r.heapIndex = len(*h)
	*h = append(*h, r)
}

func (h *reservationHeap) Pop() any {
	old := *h
	n := len(old)
	r := old[n-1]
	old[n-1] = nil
	r.heapIndex = -1
	*h = old[:n-1]
	return r
}

// stockBucket 是某仓库某商品的库存视图：现货、计划入库与预留。
type stockBucket struct {
	// owner 为所属仓库号，用于从预留记录反查仓库（明细查询）。
	owner  string
	onHand int64
	// inbounds 存放尚未确认到货的计划入库，键为入库单号。
	inbounds map[string]inbound
	// res 为预留到期最小堆；reserved 为堆中预留数量之和（增量维护，
	// 避免每次 ATP 都遍历全部预留）。
	res      reservationHeap
	reserved int64
}

func newStockBucket() *stockBucket {
	return &stockBucket{inbounds: make(map[string]inbound)}
}

// purgeExpired 惰性清除在 now 时刻已失效的预留（到期时刻 <= now，
// 即“恰到到期时刻即失效”）。每条失效预留只在此处被考察一次，
// 随后被物理移出堆，之后任何承诺判定都不会再看到它。
func (b *stockBucket) purgeExpired(now int64, m *metrics) {
	for len(b.res) > 0 {
		top := b.res[0]
		m.reservationsExamined++
		if top.expireAt > now {
			break
		}
		heap.Pop(&b.res)
		b.reserved -= top.qty
	}
}

// atp 计算承诺时刻 at 的可承诺量：
// 现货 + 到货时刻不晚于 at 的计划入库 - 当前时刻 now 下仍有效的预留。
// 预留有效性以当前时刻 now 判定（严格小于到期时刻才有效）。
func (b *stockBucket) atp(at, now int64, m *metrics) int64 {
	b.purgeExpired(now, m)
	total := b.onHand
	for _, in := range b.inbounds {
		if in.arrivalAt <= at {
			total += in.qty
		}
	}
	return total - b.reserved
}

// totalSupply 返回该桶的总供给：现货 + 全部计划入库（忽略预留与到货时刻），
// 用于永久/暂时缺货的判定。
func (b *stockBucket) totalSupply() int64 {
	total := b.onHand
	for _, in := range b.inbounds {
		total += in.qty
	}
	return total
}

// addReservation 将预留挂入本桶。
func (b *stockBucket) addReservation(r *reservation) {
	r.bucket = b
	heap.Push(&b.res, r)
	b.reserved += r.qty
}

// removeReservation 将预留从本桶摘除（确认出库或释放时调用）。
func (b *stockBucket) removeReservation(r *reservation) {
	if r.heapIndex >= 0 {
		heap.Remove(&b.res, r.heapIndex)
		b.reserved -= r.qty
	}
}

// Warehouse 是一个仓库：有全局唯一的优先序号（小者优先），
// 并按商品维护若干库存桶。
type Warehouse struct {
	id       string
	priority int
	buckets  map[string]*stockBucket
}

func newWarehouse(id string, priority int) *Warehouse {
	return &Warehouse{id: id, priority: priority, buckets: make(map[string]*stockBucket)}
}

// bucket 返回某商品的库存桶，不存在则创建。
func (w *Warehouse) bucket(product string) *stockBucket {
	b, ok := w.buckets[product]
	if !ok {
		b = newStockBucket()
		b.owner = w.id
		w.buckets[product] = b
	}
	return b
}

// findBucket 只读查找某商品的库存桶，不存在返回 nil。
func (w *Warehouse) findBucket(product string) *stockBucket {
	return w.buckets[product]
}
