package consignment

import (
	"math"
	"sort"
)

// batch 是一个寄售批次：属于某供应商与某商品，
// 记录到货时刻、到货数量、剩余量与寄售期限。
type batch struct {
	id        uint64
	supplier  uint64
	item      uint64
	arrival   int64  // 到货时刻
	period    int64  // 寄售期限（秒），到期时刻 = arrival + period
	qty       uint64 // 到货数量（不变）
	remaining uint64 // 剩余量，恒满足 0 <= remaining <= qty
	inActive  bool   // 是否仍在可领用索引中
}

// expiredAt 是时刻的纯函数：自到货时刻起恰好满期限即视为到期。
func (b *batch) expiredAt(t int64) bool {
	return t >= b.arrival+b.period
}

// allocation 是一次领用对单个批次的预分配结果。
type allocation struct {
	b    *batch
	take uint64
}

// expiryEntry 是到期索引的一条记录：某批次在 expiry 时刻到期。
// 记录是惰性的：批次耗尽或复入时旧记录不删除，弹出时按 inActive 判活。
type expiryEntry struct {
	expiry int64
	id     uint64
}

// itemStock 是某商品的可领用索引。
type itemStock struct {
	active []*batch      // 剩余量 > 0 且未到期，按 (到货时刻, 供应商编号, 批次号) 升序
	expiry []expiryEntry // active 中批次按 (到期时刻, 批次号) 升序
}

// batchLess 是领用分配顺序：到货时刻早者先，并列取供应商编号小者，再并列取批次号小者。
func batchLess(a, b *batch) bool {
	if a.arrival != b.arrival {
		return a.arrival < b.arrival
	}
	if a.supplier != b.supplier {
		return a.supplier < b.supplier
	}
	return a.id < b.id
}

// inventory 管理批次、在库上限与可领用索引。
//
// 可领用索引按（商品）维护两个有序结构：
//   - active：剩余量 > 0 且未到期的批次，按（到货时刻, 供应商编号, 批次号）排序；
//   - expiry：active 中批次按到期时刻排序的索引，用于把领用时刻之前
//     已到期的批次一次性摘出。
//
// 已耗尽批次在耗尽时即从 active 移除；已到期批次在首次被某次领用
// 发现时从 active 摘除且永不复入（时钟单调，到期不可逆）。
// 因此单次领用考察的批次记录数 = 新到期批次数 + 实际分配涉及的批次数，
// 不随已耗尽批次、已到期批次或历史结算行总数增长。
type inventory struct {
	batches map[uint64]*batch
	caps    map[pairKey]uint64 // 在库上限，未设置视为不限
	onHand  map[pairKey]uint64 // 在库量（含已到期未退回部分）
	stocks  map[uint64]*itemStock
	byPair  map[pairKey][]*batch // 供在库查询
}

func newInventory() *inventory {
	return &inventory{
		batches: make(map[uint64]*batch),
		caps:    make(map[pairKey]uint64),
		onHand:  make(map[pairKey]uint64),
		stocks:  make(map[uint64]*itemStock),
		byPair:  make(map[pairKey][]*batch),
	}
}

func (inv *inventory) get(id uint64) *batch {
	return inv.batches[id]
}

func (inv *inventory) capOf(supplier, item uint64) uint64 {
	if c, ok := inv.caps[pairKey{supplier: supplier, item: item}]; ok {
		return c
	}
	return math.MaxUint64
}

func (inv *inventory) setCap(supplier, item, cap uint64) {
	inv.caps[pairKey{supplier: supplier, item: item}] = cap
}

func (inv *inventory) onHandOf(supplier, item uint64) uint64 {
	return inv.onHand[pairKey{supplier: supplier, item: item}]
}

func (inv *inventory) stockOf(item uint64) *itemStock {
	st := inv.stocks[item]
	if st == nil {
		st = &itemStock{}
		inv.stocks[item] = st
	}
	return st
}

// insertActive 把批次插入可领用索引（调用方保证其未到期且剩余量 > 0）。
func (inv *inventory) insertActive(st *itemStock, b *batch) {
	i := sort.Search(len(st.active), func(i int) bool { return !batchLess(st.active[i], b) })
	st.active = append(st.active, nil)
	copy(st.active[i+1:], st.active[i:])
	st.active[i] = b
	b.inActive = true
	e := expiryEntry{expiry: b.arrival + b.period, id: b.id}
	j := sort.Search(len(st.expiry), func(j int) bool {
		return st.expiry[j].expiry > e.expiry ||
			(st.expiry[j].expiry == e.expiry && st.expiry[j].id >= e.id)
	})
	st.expiry = append(st.expiry, expiryEntry{})
	copy(st.expiry[j+1:], st.expiry[j:])
	st.expiry[j] = e
}

// removeActive 把批次从可领用索引摘除（调用方保证其在索引中）。
func (inv *inventory) removeActive(st *itemStock, b *batch) {
	i := sort.Search(len(st.active), func(i int) bool { return !batchLess(st.active[i], b) })
	if i < len(st.active) && st.active[i].id == b.id {
		st.active = append(st.active[:i], st.active[i+1:]...)
	}
	b.inActive = false
}

// arrive 登记到货批次。调用方已完成上限校验。
func (inv *inventory) arrive(id, supplier, item uint64, qty uint64, period int64, t int64) *batch {
	b := &batch{
		id: id, supplier: supplier, item: item,
		arrival: t, period: period,
		qty: qty, remaining: qty,
	}
	inv.batches[id] = b
	key := pairKey{supplier: supplier, item: item}
	inv.onHand[key] += qty
	inv.byPair[key] = append(inv.byPair[key], b)
	if !b.expiredAt(t) {
		inv.insertActive(inv.stockOf(item), b)
	}
	return b
}

// removeQty 从批次剩余量中减去 qty（退回或领用提交），必要时维护索引。
func (inv *inventory) removeQty(b *batch, qty uint64) {
	b.remaining -= qty
	inv.onHand[pairKey{supplier: b.supplier, item: b.item}] -= qty
	if b.remaining == 0 && b.inActive {
		inv.removeActive(inv.stockOf(b.item), b)
	}
}

// restore 把数量冲销回原批次。批次在时刻 t 未到期且不在索引中时复入索引。
func (inv *inventory) restore(b *batch, qty uint64, t int64) {
	b.remaining += qty
	inv.onHand[pairKey{supplier: b.supplier, item: b.item}] += qty
	if !b.inActive && !b.expiredAt(t) {
		inv.insertActive(inv.stockOf(b.item), b)
	}
}

// purgeExpired 把时刻 t 已到期的批次从可领用索引物理摘除，返回扫描的索引记录数。
// 只允许以不超过当前时钟的 t 调用：时钟单调保证到期不可逆，
// 被摘除的批次对任何后续操作（时刻 >= 当前时钟）都已到期，永不复入，
// 故每条索引记录至多被扫描一次。
func (inv *inventory) purgeExpired(st *itemStock, t int64) int {
	n := 0
	for len(st.expiry) > 0 && st.expiry[0].expiry <= t {
		e := st.expiry[0]
		st.expiry = st.expiry[1:]
		n++
		if b := inv.batches[e.id]; b != nil && b.inActive {
			inv.removeActive(st, b)
		}
	}
	return n
}

// allocate 为领用做预分配：纯考察，不修改任何状态，
// 对时刻 t 已到期的批次只做逻辑跳过（物理清理由 purgeExpired 在安全时刻完成）。
// 返回预分配、考察的未到期批次数与逻辑跳过的到期批次数，以便验证复杂度。
func (inv *inventory) allocate(item uint64, qty uint64, t int64) (allocs []allocation, examined, skipped int) {
	st := inv.stockOf(item)
	need := qty
	for _, b := range st.active {
		if need == 0 {
			break
		}
		if b.expiredAt(t) {
			skipped++
			continue
		}
		examined++
		take := b.remaining
		if take > need {
			take = need
		}
		allocs = append(allocs, allocation{b: b, take: take})
		need -= take
	}
	return allocs, examined, skipped
}

// commit 在领用被接受后应用预分配。
func (inv *inventory) commit(allocs []allocation) {
	for _, a := range allocs {
		inv.removeQty(a.b, a.take)
	}
}

// onHandAndExpired 查询某供应商某商品的在库量及其中已到期部分（时刻 t 判定）。
func (inv *inventory) onHandAndExpired(supplier, item uint64, t int64) (total, expired uint64) {
	for _, b := range inv.byPair[pairKey{supplier: supplier, item: item}] {
		total += b.remaining
		if b.remaining > 0 && b.expiredAt(t) {
			expired += b.remaining
		}
	}
	return total, expired
}
