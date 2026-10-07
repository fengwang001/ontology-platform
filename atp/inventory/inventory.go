// Package inventory 维护多仓库存状态：现货、计划入库、预留，
// 并提供可承诺量计算与预留生命周期管理（确认出库、释放、懒过期）。
//
// 关键设计：每个 (仓库, 商品) 的预留按到期时刻组织为最小堆，
// 另维护一个“仍有效预留总量”的滚动和。可承诺量计算只需 O(1)
// 读取滚动和，并对堆顶做懒过期清理；每条失效预留最多被考察一次，
// 因此考察次数不随历史订单总数或已失效预留数增长（摊还 O(1)）。
package inventory

import (
	"container/heap"
	"fmt"
	"sort"

	"ontology/atp/clock"
	"ontology/atp/reject"
)

// Warehouse 为一个仓库。优先序号小者优先。
type Warehouse struct {
	ID       string
	Priority int
	stocks   map[string]*stockState
}

// OrderRecord 为一张订单的全部预留。
type OrderRecord struct {
	ID     string
	Expiry clock.Time
	Res    []*Reservation
}

// OrderDetail 为订单预留明细的只读视图。
type OrderDetail struct {
	OrderID string
	Expiry  clock.Time
	// Valid 表示在当前时刻预留是否仍有效（now < Expiry）。
	Valid bool
	Lines []OrderDetailLine
}

// OrderDetailLine 为一条预留明细。
type OrderDetailLine struct {
	Warehouse string
	SKU       string
	Qty       int64
}

// Inventory 为全部仓库与订单预留的集合。
type Inventory struct {
	whs    map[string]*Warehouse
	sorted []*Warehouse // 按 (Priority, ID) 排序，保证确定性
	orders map[string]*OrderRecord

	// examined 统计可承诺量计算过程中考察的预留记录数，
	// 用于验证考察次数不随历史增长。
	examined uint64
	// sweeps 统计懒过期扫描次数。
	sweeps uint64
}

// New 创建空库存。
func New() *Inventory {
	return &Inventory{
		whs:    make(map[string]*Warehouse),
		sorted: nil,
		orders: make(map[string]*OrderRecord),
	}
}

// Stats 返回考察计数（examined）与扫描次数（sweeps）。
func (inv *Inventory) Stats() (examined, sweeps uint64) {
	return inv.examined, inv.sweeps
}

// AddWarehouse 新增仓库；ID 重复报参数非法。
func (inv *Inventory) AddWarehouse(id string, priority int) error {
	if _, ok := inv.whs[id]; ok {
		return reject.New(reject.InvalidParam, -1, "warehouse already exists: "+id)
	}
	w := &Warehouse{ID: id, Priority: priority, stocks: make(map[string]*stockState)}
	inv.whs[id] = w
	inv.sorted = append(inv.sorted, w)
	sort.Slice(inv.sorted, func(i, j int) bool {
		if inv.sorted[i].Priority != inv.sorted[j].Priority {
			return inv.sorted[i].Priority < inv.sorted[j].Priority
		}
		return inv.sorted[i].ID < inv.sorted[j].ID
	})
	return nil
}

// Sorted 返回按优先序号（并列按 ID）排序的仓库列表，保证确定性。
func (inv *Inventory) Sorted() []*Warehouse { return inv.sorted }

// Warehouse 按 ID 查找仓库。
func (inv *Inventory) Warehouse(id string) (*Warehouse, bool) {
	w, ok := inv.whs[id]
	return w, ok
}

func (w *Warehouse) stock(sku string, create bool) *stockState {
	if s, ok := w.stocks[sku]; ok {
		return s
	}
	if !create {
		return nil
	}
	s := &stockState{warehouse: w}
	w.stocks[sku] = s
	return s
}

// AddStock 增加现货。
func (inv *Inventory) AddStock(whID, sku string, qty int64) error {
	w, ok := inv.whs[whID]
	if !ok {
		return reject.New(reject.InvalidParam, -1, "unknown warehouse: "+whID)
	}
	w.stock(sku, true).onHand += qty
	return nil
}

// ScheduleInbound 登记一条计划入库。
func (inv *Inventory) ScheduleInbound(whID, sku string, in *Inbound) error {
	w, ok := inv.whs[whID]
	if !ok {
		return reject.New(reject.InvalidParam, -1, "unknown warehouse: "+whID)
	}
	s := w.stock(sku, true)
	for _, e := range s.inbounds {
		if e.ID == in.ID {
			return reject.New(reject.InvalidParam, -1, "inbound id already exists: "+in.ID)
		}
	}
	s.inbounds = append(s.inbounds, in)
	return nil
}

// ConfirmInbound 确认到货：把计划入库转为现货。
// 只允许对尚未确认、且到货时刻不晚于 now 的计划入库进行。
func (inv *Inventory) ConfirmInbound(whID, sku, inboundID string, now clock.Time) error {
	w, ok := inv.whs[whID]
	if !ok {
		return reject.New(reject.InvalidParam, -1, "unknown warehouse: "+whID)
	}
	s := w.stock(sku, false)
	if s == nil {
		return reject.New(reject.InvalidParam, -1, "unknown inbound: "+inboundID)
	}
	for i, in := range s.inbounds {
		if in.ID == inboundID {
			if in.Arrival > now {
				return reject.New(reject.InvalidParam, -1,
					fmt.Sprintf("inbound %s arrives at %d, not yet arrived at %d", inboundID, in.Arrival, now))
			}
			s.onHand += in.Qty
			s.inbounds = append(s.inbounds[:i], s.inbounds[i+1:]...)
			return nil
		}
	}
	return reject.New(reject.InvalidParam, -1, "unknown or already confirmed inbound: "+inboundID)
}

// Available 计算某仓某商品的可承诺量：现货 + 到货时刻不晚于 cutoff
// 的未确认入库 - 在 resNow 时刻仍有效的预留。
func (inv *Inventory) Available(whID, sku string, cutoff, resNow clock.Time) int64 {
	w, ok := inv.whs[whID]
	if !ok {
		return 0
	}
	s := w.stock(sku, false)
	if s == nil {
		return 0
	}
	inv.sweeps++
	s.reservations.sweep(resNow, &inv.examined)
	return s.available(cutoff)
}

// TotalSupply 计算某商品在所有仓库的总供给：
// 现货 + 全部未确认计划入库（忽略预留与到货时刻）。
func (inv *Inventory) TotalSupply(sku string) int64 {
	var total int64
	for _, w := range inv.sorted {
		if s := w.stock(sku, false); s != nil {
			total += s.onHand + s.inboundTotal()
		}
	}
	return total
}

// HasActiveOrder 判断订单是否存在且在 now 时刻仍有效的预留。
func (inv *Inventory) HasActiveOrder(orderID string, now clock.Time) bool {
	rec, ok := inv.orders[orderID]
	return ok && now < rec.Expiry
}

// CreateOrder 登记一张订单的全部预留（承诺成功时调用）。
func (inv *Inventory) CreateOrder(rec *OrderRecord) {
	for _, r := range rec.Res {
		r.state = resvActive
		r.stock.activeReserved += r.Qty
		heap.Push(&r.stock.reservations, r)
	}
	inv.orders[rec.ID] = rec
}

// NewReservation 为承诺引擎构造一条预留记录（尚未生效）。
func (inv *Inventory) NewReservation(orderID, whID, sku string, qty int64, expiry clock.Time) *Reservation {
	w := inv.whs[whID]
	return &Reservation{
		OrderID: orderID,
		SKU:     sku,
		Qty:     qty,
		Expiry:  expiry,
		stock:   w.stock(sku, true),
	}
}

// GetOrder 返回订单预留明细；订单不存在报 OrderNotFound。
// 是否有效以 now（当前时钟）判定。
func (inv *Inventory) GetOrder(orderID string, now clock.Time) (*OrderDetail, error) {
	rec, ok := inv.orders[orderID]
	if !ok {
		return nil, reject.New(reject.OrderNotFound, -1, "order not found: "+orderID)
	}
	d := &OrderDetail{
		OrderID: rec.ID,
		Expiry:  rec.Expiry,
		Valid:   now < rec.Expiry,
	}
	for _, r := range rec.Res {
		d.Lines = append(d.Lines, OrderDetailLine{
			Warehouse: warehouseOf(r),
			SKU:       r.SKU,
			Qty:       r.Qty,
		})
	}
	return d, nil
}

func warehouseOf(r *Reservation) string { return r.stock.warehouse.ID }

// ConfirmOutbound 确认出库：把仍有效的预留转为现货扣减。
// 订单不存在报 OrderNotFound；预留已到期报 ReservationExpired。
func (inv *Inventory) ConfirmOutbound(orderID string, now clock.Time) error {
	rec, ok := inv.orders[orderID]
	if !ok {
		return reject.New(reject.OrderNotFound, -1, "order not found: "+orderID)
	}
	if now >= rec.Expiry {
		return reject.New(reject.ReservationExpired, -1,
			fmt.Sprintf("order %s reservation expired at %d, now %d", orderID, rec.Expiry, now))
	}
	for _, r := range rec.Res {
		s := r.stock
		// 把已到货未确认的入库转为现货，保证扣减后现货非负。
		s.foldArrivals(now)
		s.onHand -= r.Qty
		if s.onHand < 0 {
			panic(fmt.Sprintf("invariant violated: negative on-hand %d at order %s", s.onHand, orderID))
		}
		s.activeReserved -= r.Qty
		r.state = resvConsumed
	}
	delete(inv.orders, orderID)
	return nil
}

// Release 释放仍有效的预留。订单不存在或预留已到期均报 OrderNotFound。
func (inv *Inventory) Release(orderID string, now clock.Time) error {
	rec, ok := inv.orders[orderID]
	if !ok {
		return reject.New(reject.OrderNotFound, -1, "order not found: "+orderID)
	}
	if now >= rec.Expiry {
		return reject.New(reject.OrderNotFound, -1,
			fmt.Sprintf("order %s reservation already expired at %d", orderID, rec.Expiry))
	}
	for _, r := range rec.Res {
		r.stock.activeReserved -= r.Qty
		r.state = resvReleased
	}
	delete(inv.orders, orderID)
	return nil
}

// CheckInvariants 校验全局不变量（供测试与调试）：
// 现货非负；有效预留之和不超过可承诺上限（可承诺量非负）。
func (inv *Inventory) CheckInvariants(now clock.Time) error {
	for _, w := range inv.sorted {
		for sku, s := range w.stocks {
			s.reservations.sweep(now, &inv.examined)
			if s.onHand < 0 {
				return fmt.Errorf("warehouse %s sku %s: negative on-hand %d", w.ID, sku, s.onHand)
			}
			if s.activeReserved < 0 {
				return fmt.Errorf("warehouse %s sku %s: negative active reserved %d", w.ID, sku, s.activeReserved)
			}
			if s.available(now) < 0 {
				return fmt.Errorf("warehouse %s sku %s: active reserved %d exceeds ATP cap", w.ID, sku, s.activeReserved)
			}
		}
	}
	return nil
}
