// Package naive 是库存承诺系统的朴素参照模型，用于与正式实现做随机对照测试。
//
// 它刻意采用最直接、显然正确的写法：预留与计划入库全部保存在普通切片里，
// 每次 ATP 都全量扫描历史记录，失效预留从不清理。因此它的预留考察次数
// 随历史订单数线性增长——这正好反衬正式实现中“惰性过期 + 增量求和”的界。
// 语义必须与 inventory.System 完全一致。
package naive

import (
	"fmt"
	"sort"

	"ontology/inventory"
)

type inbound struct {
	arrivalAt int64
	qty       int64
	arrived   bool
}

type reservation struct {
	qty      int64
	expireAt int64
	active   bool // 确认出库或释放后置 false；到期不清理，仅在求和时忽略
}

type bucket struct {
	onHand int64
	// inbounds 键为入库单号。
	inbounds map[string]*inbound
	// reservations 追加式保存全部预留（含失效/已消费的），从不删除。
	reservations []*reservation
}

type order struct {
	expireAt int64
	recs     []*reservation
}

// System 是朴素模型的状态机。
type System struct {
	clock      int64
	priorities map[string]int
	warehouses []string
	buckets    map[string]map[string]*bucket // 仓库 -> 商品 -> 桶
	orders     map[string]*order
	// Examined 统计 ATP 过程中扫描过的预留记录条数，用于与正式实现对照。
	Examined int64
}

// New 创建朴素模型。
func New() *System {
	return &System{
		priorities: make(map[string]int),
		buckets:    make(map[string]map[string]*bucket),
		orders:     make(map[string]*order),
	}
}

// Clock 返回当前时刻。
func (s *System) Clock() int64 { return s.clock }

// AddWarehouse 注册仓库（优先序号小者优先）。
func (s *System) AddWarehouse(id string, priority int) *inventory.Error {
	if id == "" || priority < 0 {
		return &inventory.Error{Reason: inventory.ReasonInvalidParam, Line: -1}
	}
	if _, ok := s.priorities[id]; ok {
		return &inventory.Error{Reason: inventory.ReasonInvalidParam, Line: -1}
	}
	for _, p := range s.priorities {
		if p == priority {
			return &inventory.Error{Reason: inventory.ReasonInvalidParam, Line: -1}
		}
	}
	s.priorities[id] = priority
	s.warehouses = append(s.warehouses, id)
	s.buckets[id] = make(map[string]*bucket)
	return nil
}

func (s *System) bucketOf(wh, product string) *bucket {
	b, ok := s.buckets[wh][product]
	if !ok {
		b = &bucket{inbounds: make(map[string]*inbound)}
		s.buckets[wh][product] = b
	}
	return b
}

func (s *System) sortedWarehouses() []string {
	ws := append([]string(nil), s.warehouses...)
	sort.Slice(ws, func(i, j int) bool { return s.priorities[ws[i]] < s.priorities[ws[j]] })
	return ws
}

func (s *System) checkClock(now int64) *inventory.Error {
	if now < s.clock {
		return &inventory.Error{Reason: inventory.ReasonClockRollback, Line: -1}
	}
	return nil
}

// AddOnHand 增加现货。
func (s *System) AddOnHand(wh, product string, qty, now int64) *inventory.Error {
	if _, ok := s.priorities[wh]; !ok {
		return &inventory.Error{Reason: inventory.ReasonInvalidParam, Line: -1}
	}
	if product == "" || qty <= 0 || now < 0 {
		return &inventory.Error{Reason: inventory.ReasonInvalidParam, Line: -1}
	}
	if err := s.checkClock(now); err != nil {
		return err
	}
	s.bucketOf(wh, product).onHand += qty
	s.clock = now
	return nil
}

// AddPlannedInbound 登记计划入库。
func (s *System) AddPlannedInbound(wh, product, inboundID string, arrivalAt, qty, now int64) *inventory.Error {
	if _, ok := s.priorities[wh]; !ok {
		return &inventory.Error{Reason: inventory.ReasonInvalidParam, Line: -1}
	}
	if product == "" || inboundID == "" || arrivalAt < 0 || qty <= 0 || now < 0 {
		return &inventory.Error{Reason: inventory.ReasonInvalidParam, Line: -1}
	}
	if err := s.checkClock(now); err != nil {
		return err
	}
	b := s.bucketOf(wh, product)
	if _, ok := b.inbounds[inboundID]; ok {
		return &inventory.Error{Reason: inventory.ReasonInvalidParam, Line: -1}
	}
	b.inbounds[inboundID] = &inbound{arrivalAt: arrivalAt, qty: qty}
	s.clock = now
	return nil
}

// ConfirmArrival 确认到货：计划入库转为现货，不得重复确认。
func (s *System) ConfirmArrival(wh, product, inboundID string, now int64) *inventory.Error {
	if _, ok := s.priorities[wh]; !ok {
		return &inventory.Error{Reason: inventory.ReasonInvalidParam, Line: -1}
	}
	if inboundID == "" || now < 0 {
		return &inventory.Error{Reason: inventory.ReasonInvalidParam, Line: -1}
	}
	if err := s.checkClock(now); err != nil {
		return err
	}
	b, ok := s.buckets[wh][product]
	if !ok {
		return &inventory.Error{Reason: inventory.ReasonInboundNotFound, Line: -1}
	}
	in, ok := b.inbounds[inboundID]
	if !ok || in.arrived {
		return &inventory.Error{Reason: inventory.ReasonInboundNotFound, Line: -1}
	}
	in.arrived = true
	b.onHand += in.qty
	s.clock = now
	return nil
}

// atp 全量扫描：现货 + 到货时刻不晚于 at 且未到货的计划入库 - 当前有效的预留。
func (s *System) atp(wh, product string, at int64) int64 {
	b, ok := s.buckets[wh][product]
	if !ok {
		return 0
	}
	total := b.onHand
	for _, in := range b.inbounds {
		if !in.arrived && in.arrivalAt <= at {
			total += in.qty
		}
	}
	for _, r := range b.reservations {
		s.Examined++
		if r.active && s.clock < r.expireAt {
			total -= r.qty
		}
	}
	return total
}

// totalSupply 现货 + 全部计划入库（忽略预留与到货时刻）。
func (s *System) totalSupply(product string) int64 {
	var total int64
	for _, wh := range s.warehouses {
		if b, ok := s.buckets[wh][product]; ok {
			total += b.onHand
			for _, in := range b.inbounds {
				if !in.arrived {
					total += in.qty
				}
			}
		}
	}
	return total
}

// CommitOrder 承诺订单：整单全有或全无，语义与 inventory.System 一致。
func (s *System) CommitOrder(orderID string, lines []inventory.OrderLine, commitAt int64, allowSplit bool, reserveFor int64, maxWarehouses int) (*inventory.CommitResult, *inventory.Error) {
	if orderID == "" || len(lines) == 0 || commitAt < 0 || reserveFor < 0 || maxWarehouses < 1 {
		return nil, &inventory.Error{Reason: inventory.ReasonInvalidParam, Line: -1}
	}
	for _, ln := range lines {
		if ln.Product == "" || ln.Qty <= 0 {
			return nil, &inventory.Error{Reason: inventory.ReasonInvalidParam, Line: -1}
		}
	}
	if err := s.checkClock(commitAt); err != nil {
		return nil, err
	}
	if ord, ok := s.orders[orderID]; ok && commitAt < ord.expireAt {
		return nil, &inventory.Error{Reason: inventory.ReasonDuplicateOrder, Line: -1}
	}

	ws := s.sortedWarehouses()
	type key struct{ wh, product string }
	remaining := make(map[key]int64)
	avail := func(wh, product string) int64 {
		k := key{wh, product}
		v, ok := remaining[k]
		if !ok {
			v = s.atp(wh, product, commitAt)
			remaining[k] = v
		}
		return v
	}
	consumed := make(map[string]int64)
	var allocs []inventory.Allocation
	failLine, permanent := -1, false

	for i, line := range lines {
		if !allowSplit {
			chosen := ""
			for _, wh := range ws {
				if avail(wh, line.Product) >= line.Qty {
					chosen = wh
					break
				}
			}
			if chosen == "" {
				failLine = i
				permanent = s.totalSupply(line.Product)-consumed[line.Product] < line.Qty
				break
			}
			remaining[key{chosen, line.Product}] -= line.Qty
			allocs = append(allocs, inventory.Allocation{LineIndex: i, Warehouse: chosen, Product: line.Product, Qty: line.Qty})
		} else {
			need := line.Qty
			for _, wh := range ws {
				if need == 0 {
					break
				}
				a := avail(wh, line.Product)
				if a <= 0 {
					continue
				}
				q := a
				if q > need {
					q = need
				}
				remaining[key{wh, line.Product}] -= q
				allocs = append(allocs, inventory.Allocation{LineIndex: i, Warehouse: wh, Product: line.Product, Qty: q})
				need -= q
			}
			if need > 0 {
				failLine = i
				permanent = s.totalSupply(line.Product)-consumed[line.Product] < line.Qty
				break
			}
		}
		consumed[line.Product] += line.Qty
	}

	if failLine >= 0 {
		reason := inventory.ReasonTemporaryShortage
		if permanent {
			reason = inventory.ReasonPermanentShortage
		}
		return nil, &inventory.Error{Reason: reason, Line: failLine}
	}
	used := make(map[string]bool)
	for _, a := range allocs {
		used[a.Warehouse] = true
	}
	if len(used) > maxWarehouses {
		return nil, &inventory.Error{Reason: inventory.ReasonTooManySplits, Line: -1}
	}

	expireAt := commitAt + reserveFor
	ord := &order{expireAt: expireAt}
	for _, a := range allocs {
		r := &reservation{qty: a.Qty, expireAt: expireAt, active: true}
		s.bucketOf(a.Warehouse, a.Product).reservations = append(s.bucketOf(a.Warehouse, a.Product).reservations, r)
		ord.recs = append(ord.recs, r)
	}
	s.orders[orderID] = ord
	s.clock = commitAt
	return &inventory.CommitResult{OrderID: orderID, ExpireAt: expireAt, Allocations: allocs}, nil
}

// ConfirmOutbound 确认出库：有效预留转为现货扣减。
func (s *System) ConfirmOutbound(orderID string, now int64) *inventory.Error {
	if orderID == "" || now < 0 {
		return &inventory.Error{Reason: inventory.ReasonInvalidParam, Line: -1}
	}
	if err := s.checkClock(now); err != nil {
		return err
	}
	ord, ok := s.orders[orderID]
	if !ok {
		return &inventory.Error{Reason: inventory.ReasonOrderNotFound, Line: -1}
	}
	if now >= ord.expireAt {
		return &inventory.Error{Reason: inventory.ReasonReservationExpired, Line: -1}
	}
	// 找到每条预留所在桶以校验现货充足（朴素做法：记录时未存桶指针，按商品逐仓查找）。
	for _, r := range ord.recs {
		if !r.active {
			continue
		}
		b := s.findBucketOf(r)
		if b.onHand < r.qty {
			return &inventory.Error{Reason: inventory.ReasonInsufficientOnHand, Line: -1}
		}
	}
	for _, r := range ord.recs {
		if !r.active {
			continue
		}
		b := s.findBucketOf(r)
		b.onHand -= r.qty
		r.active = false
	}
	delete(s.orders, orderID)
	s.clock = now
	return nil
}

// findBucketOf 线性反查预留所属桶（朴素模型不维护反向索引）。
func (s *System) findBucketOf(target *reservation) *bucket {
	for _, perProduct := range s.buckets {
		for _, b := range perProduct {
			for _, r := range b.reservations {
				if r == target {
					return b
				}
			}
		}
	}
	panic(fmt.Sprintf("naive: 预留记录 %p 不属于任何桶", target))
}

// ReleaseReservation 释放仍有效的预留；已到期或不存在报订单不存在。
func (s *System) ReleaseReservation(orderID string, now int64) *inventory.Error {
	if orderID == "" || now < 0 {
		return &inventory.Error{Reason: inventory.ReasonInvalidParam, Line: -1}
	}
	if err := s.checkClock(now); err != nil {
		return err
	}
	ord, ok := s.orders[orderID]
	if !ok || now >= ord.expireAt {
		return &inventory.Error{Reason: inventory.ReasonOrderNotFound, Line: -1}
	}
	for _, r := range ord.recs {
		r.active = false
	}
	delete(s.orders, orderID)
	s.clock = now
	return nil
}

// ATP 查询可承诺量（只读，不推进时钟）。
func (s *System) ATP(wh, product string, at int64) (int64, *inventory.Error) {
	if _, ok := s.priorities[wh]; !ok {
		return 0, &inventory.Error{Reason: inventory.ReasonInvalidParam, Line: -1}
	}
	if product == "" || at < 0 {
		return 0, &inventory.Error{Reason: inventory.ReasonInvalidParam, Line: -1}
	}
	if at < s.clock {
		return 0, &inventory.Error{Reason: inventory.ReasonInvalidParam, Line: -1}
	}
	return s.atp(wh, product, at), nil
}

// ReservationDetails 查询订单预留明细。
func (s *System) ReservationDetails(orderID string) ([]inventory.ReservationDetail, *inventory.Error) {
	if orderID == "" {
		return nil, &inventory.Error{Reason: inventory.ReasonInvalidParam, Line: -1}
	}
	ord, ok := s.orders[orderID]
	if !ok {
		return nil, &inventory.Error{Reason: inventory.ReasonOrderNotFound, Line: -1}
	}
	valid := s.clock < ord.expireAt
	var details []inventory.ReservationDetail
	for _, r := range ord.recs {
		// 朴素模型未在预留上记录仓库与商品，逐仓逐商品反查。
		wh, product := s.findOwnerOf(r)
		details = append(details, inventory.ReservationDetail{
			Warehouse: wh, Product: product, Qty: r.qty, ExpireAt: r.expireAt, Valid: valid,
		})
	}
	return details, nil
}

func (s *System) findOwnerOf(target *reservation) (string, string) {
	for wh, perProduct := range s.buckets {
		for product, b := range perProduct {
			for _, r := range b.reservations {
				if r == target {
					return wh, product
				}
			}
		}
	}
	panic("naive: 预留记录无所属桶")
}
