package inventory

import "container/heap"

import "ontology/atp/clock"

// resvState 为预留的生命周期状态。
type resvState int

const (
	resvActive   resvState = iota // 有效（是否过期由时刻判定）
	resvConsumed                  // 已确认出库
	resvReleased                  // 已释放
	resvExpired                   // 已被懒清理扫描到并移除
)

// Reservation 为一条预留记录。同一订单在同一仓库同一商品上的一段承诺。
type Reservation struct {
	OrderID string
	SKU     string
	Qty     int64
	Expiry  clock.Time
	state   resvState
	stock   *stockState // 所属库存状态，便于出库/释放时直接定位
}

// resvHeap 按到期时刻组织的最小堆。堆中可能含有已消费/已释放的
// “墓碑”记录，它们不再计入 activeReserved，待懒清理时移除。
type resvHeap []*Reservation

func (h resvHeap) Len() int            { return len(h) }
func (h resvHeap) Less(i, j int) bool  { return h[i].Expiry < h[j].Expiry }
func (h resvHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *resvHeap) Push(x interface{}) { *h = append(*h, x.(*Reservation)) }
func (h *resvHeap) Pop() interface{} {
	old := *h
	n := len(old)
	r := old[n-1]
	old[n-1] = nil
	*h = old[:n-1]
	return r
}

// sweep 移除所有在 now 时刻已到期（Expiry <= now）的预留。
// 每考察一条记录（含判断后停止的那一次堆顶查看）都通过计数器
// 上报，用于证明考察次数不随历史总量增长：每条失效记录最多被
// 弹出一次，此后不再出现。
func (h *resvHeap) sweep(now clock.Time, examined *uint64) {
	for len(*h) > 0 {
		*examined++
		top := (*h)[0]
		if top.Expiry > now {
			return
		}
		heap.Pop(h)
		if top.state == resvActive {
			top.stock.activeReserved -= top.Qty
		}
		top.state = resvExpired
	}
}
