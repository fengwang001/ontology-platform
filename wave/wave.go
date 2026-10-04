// Package wave 实现波次拣货的库位预占器。
package wave

import (
	"sort"

	"ontology/reserve"
	"ontology/slot"
	"sync"
)

// 五类哨兵错误，errors.Is 可判。
var (
	ErrInvalidArg  = slot.ErrInvalidArg
	ErrNotFound    = slot.ErrNotFound
	ErrBadState    = slot.ErrBadState
	ErrQtyMismatch = slot.ErrQtyMismatch
	ErrConflict    = slot.ErrConflict
)

// Status 为订单状态。
type Status int

const (
	StatusNew Status = iota
	StatusAllocated
	StatusBackorder
	StatusDone
	StatusShort
	StatusCancelled
)

func (st Status) String() string {
	switch st {
	case StatusNew:
		return "New"
	case StatusAllocated:
		return "Allocated"
	case StatusBackorder:
		return "Backorder"
	case StatusDone:
		return "Done"
	case StatusShort:
		return "Short"
	case StatusCancelled:
		return "Cancelled"
	}
	return "?"
}

// Line 为一行需求 (sku, qty)。
type Line struct {
	SKU string
	Qty int64
}

// Alloc 为单条分配明细（库位, 数量）。
type Alloc struct {
	Loc string
	Qty int64
}

// OrderResult 为 Release 后单个订单的结果。
type OrderResult struct {
	Order string
	State Status
	Lines map[string][]Alloc // SKU → 按库位编号字节序的明细；Backorder 为空
	Err   error
}

// Probed 为单行分配考察库位数的只读计数（测试用）。
type Probed struct {
	BulkPallet int
	Pick       int
	BulkTail   int
}

type order struct {
	id       string
	priority int
	lines    []Line // 已按 SKU 字节序
	state    Status
	short    int64  // 累计缺口
	last     Probed // 最近一次分配的考察计数
}

// Coordinator 为预占器入口；一把锁串行化全部操作。
type Coordinator struct {
	mu     sync.RWMutex
	store  *slot.Store
	ledger *reserve.Ledger
	orders map[string]*order
	probed Probed
}

// New 创建预占器。
func New() *Coordinator {
	return &Coordinator{
		store:  slot.NewStore(),
		ledger: reserve.NewLedger(),
		orders: map[string]*order{},
	}
}

func validBytes1(s string) bool { return len(s) >= 1 && len(s) <= 32 }

// SetPallet 设定某 SKU 的整托量（每 SKU 仅一次）。
func (c *Coordinator) SetPallet(sku string, p int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !validBytes1(sku) || p < 1 || p > 1_000_000 {
		return ErrInvalidArg
	}
	return c.store.SetPallet(sku, p)
}

// PutStock 在库位上新设或追加库存。
func (c *Coordinator) PutStock(loc string, kind slot.Kind, sku string, qty int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !validBytes1(loc) || !validBytes1(sku) || (kind != slot.Bulk && kind != slot.Pick) ||
		qty < 1 || qty > 1_000_000_000 {
		return ErrInvalidArg
	}
	if _, ok := c.store.Pallet(sku); !ok {
		return ErrNotFound
	}
	if cur, ok := c.store.Get(loc); ok {
		if cur.Locked {
			return ErrBadState
		}
		if cur.Kind != kind || cur.SKU != sku {
			return ErrConflict
		}
	}
	return c.store.PutStock(loc, kind, sku, qty)
}

// AddOrder 新增订单（初始 New）。
func (c *Coordinator) AddOrder(id string, priority int, lines []Line) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !validBytes1(id) || priority < 0 || priority > 9 || len(lines) < 1 || len(lines) > 50 {
		return ErrInvalidArg
	}
	seen := map[string]bool{}
	for _, ln := range lines {
		if !validBytes1(ln.SKU) || ln.Qty < 1 || ln.Qty > 1_000_000_000 || seen[ln.SKU] {
			return ErrInvalidArg
		}
		seen[ln.SKU] = true
	}
	if _, ok := c.orders[id]; ok {
		return ErrConflict
	}
	sorted := append([]Line(nil), lines...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].SKU < sorted[j].SKU })
	c.orders[id] = &order{id: id, priority: priority, lines: sorted, state: StatusNew}
	return nil
}

// Unlock 盘点后解锁并重置在库。
func (c *Coordinator) Unlock(loc string, counted int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !validBytes1(loc) || counted < 0 || counted > 1_000_000_000 {
		return ErrInvalidArg
	}
	cur, ok := c.store.Get(loc)
	if !ok {
		return ErrNotFound
	}
	if !cur.Locked {
		return ErrBadState
	}
	return c.store.Unlock(loc, counted)
}

// Cancel 取消已分配订单，释放全部未拣预占。
func (c *Coordinator) Cancel(orderID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !validBytes1(orderID) {
		return ErrInvalidArg
	}
	o, ok := c.orders[orderID]
	if !ok {
		return ErrNotFound
	}
	if o.state != StatusAllocated {
		return ErrBadState
	}
	for _, r := range c.ledger.OrderLocs(orderID) {
		c.store.Release(r.Loc, r.Qty)
		c.ledger.Delete(orderID, r.Loc)
	}
	o.state = StatusCancelled
	return nil
}

// Status 返回订单状态与累计缺口。
func (c *Coordinator) Status(orderID string) (Status, int64, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if !validBytes1(orderID) {
		return 0, 0, ErrInvalidArg
	}
	o, ok := c.orders[orderID]
	if !ok {
		return 0, 0, ErrNotFound
	}
	return o.state, o.short, nil
}

// ProbedCount 返回最近一次单行分配考察的库位数（测试用）。
func (c *Coordinator) ProbedCount() Probed {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.probed
}

// snapshotOrder 返回订单全部预占明细（按库位编号字节序）。
func (c *Coordinator) OrderAllocs(orderID string) ([]Alloc, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if !validBytes1(orderID) {
		return nil, ErrInvalidArg
	}
	if _, ok := c.orders[orderID]; !ok {
		return nil, ErrNotFound
	}
	recs := c.ledger.OrderLocs(orderID)
	out := make([]Alloc, 0, len(recs))
	for _, r := range recs {
		out = append(out, Alloc{Loc: r.Loc, Qty: r.Qty})
	}
	return out, nil
}

// LocView 返回库位只读视图（测试/朴素模型用）。
func (c *Coordinator) LocView(loc string) (slot.Location, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.store.Get(loc)
}
