package inventory

import "container/heap"

// Reservation 是一条预留记录，同时被订单索引与所属桶的到期堆引用。
type Reservation struct {
	OrderID   string
	Warehouse string
	Product   string
	Qty       int64
	Expiry    int64
	alive     bool
}

// bucket 是某仓库某商品的库存视图：现货、计划入库与预留。
type bucket struct {
	onHand int64
	// inbound 是尚未确认到货的计划入库：到货时刻 -> 数量。
	inbound      map[int64]int64
	inboundTotal int64
	// reserved 是当前有效预留数量之和（运行值，随懒清理维护），
	// 使可承诺量计算不必逐条扫描预留记录。
	reserved int64
	// res 是按到期时刻的最小堆，可能含有已失效/已终止的墓碑记录，
	// 由 cleanExpired 惰性弹出，每条记录至多被考察一次。
	res resHeap
}

func newBucket() *bucket {
	return &bucket{inbound: make(map[int64]int64)}
}

// cleanExpired 弹出所有到期时刻不晚于 now 的预留记录。
// 每弹出一条记录 examined 加一，用于证明考察次数的上界。
func (b *bucket) cleanExpired(now int64, examined *int64) {
	for len(b.res) > 0 && b.res[0].Expiry <= now {
		r := heap.Pop(&b.res).(*Reservation)
		*examined++
		if r.alive {
			r.alive = false
			b.reserved -= r.Qty
		}
	}
}

// inboundArrivedBy 返回到货时刻不晚于 t 的计划入库总量（只读，不改变状态）。
func (b *bucket) inboundArrivedBy(t int64) int64 {
	var sum int64
	for arrival, qty := range b.inbound {
		if arrival <= t {
			sum += qty
		}
	}
	return sum
}

// materialize 把到货时刻不晚于 now 的计划入库转为现货，每条入库只转换一次。
func (b *bucket) materialize(now int64) {
	for arrival, qty := range b.inbound {
		if arrival <= now {
			b.onHand += qty
			b.inboundTotal -= qty
			delete(b.inbound, arrival)
		}
	}
}

// validReservedNow 以 now 判定有效性，返回有效预留之和。
// 仅供只读查询使用（查询不得改变状态，不能用懒清理代替）。
func (b *bucket) validReservedNow(now int64) int64 {
	var sum int64
	for _, r := range b.res {
		if r.alive && r.Expiry > now {
			sum += r.Qty
		}
	}
	return sum
}

// atp 计算承诺时刻的可承诺量，调用前必须已在 now 时刻做过懒清理。
func (b *bucket) atp(commitTime int64) int64 {
	return b.onHand + b.inboundArrivedBy(commitTime) - b.reserved
}
