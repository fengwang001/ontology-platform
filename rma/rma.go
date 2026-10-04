// Package rma 管理退货授权与每行可退余量。
package rma

import (
	"container/heap"
	"errors"
	"sync"
)

// 哨兵错误，可用 errors.Is 区分。
var (
	ErrInvalid  = errors.New("rma: invalid argument")
	ErrClock    = errors.New("rma: clock moved backwards")
	ErrNotFound = errors.New("rma: not found")
	ErrConflict = errors.New("rma: conflict")
	ErrWindow   = errors.New("rma: outside return window")
	ErrCapacity = errors.New("rma: insufficient returnable quantity")
	ErrExpired  = errors.New("rma: authorization expired")
)

type OrderID string
type RMAID string
type LineID int64

type Line struct {
	ID      LineID
	Shipped int64
	Paid    int64
}

type Item struct {
	Line LineID
	Qty  int64
}

type lineRec struct {
	shipped   int64
	paid      int64
	qualified int64 // 已收 A/B 件数
	reserved  int64 // 有效授权未收件数
}

type authRec struct {
	id      RMAID
	order   OrderID
	exp     int64
	items   map[LineID]int64 // 每行未收数量
	dead    bool
	heapIdx int
}

type expireHeap []*authRec

func (h expireHeap) Len() int           { return len(h) }
func (h expireHeap) Less(i, j int) bool { return h[i].exp < h[j].exp }
func (h expireHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].heapIdx = i
	h[j].heapIdx = j
}
func (h *expireHeap) Push(x any) {
	a := x.(*authRec)
	a.heapIdx = len(*h)
	*h = append(*h, a)
}
func (h *expireHeap) Pop() any {
	old := *h
	n := len(old)
	a := old[n-1]
	old[n-1] = nil
	*h = old[:n-1]
	return a
}

type orderRec struct {
	shipAt int64
	lines  map[LineID]*lineRec
}

type reservedAdj struct {
	ln  *lineRec
	qty int64
}

// pendingExpire 是一次试落地到期的结果，拒绝时可整体回滚。
type pendingExpire struct {
	expired []*authRec
	adjs    []reservedAdj
}

type RMA struct {
	window int64
	valid  int64

	mu      sync.Mutex
	lastNow int64

	orders map[OrderID]*orderRec
	auths  map[RMAID]*authRec
	due    expireHeap

	// touched 计数写操作触碰（修改）的授权单记录数，用于性能论据验证。
	touched int

	// onExpire 在授权单到期被彻底落地（从 auths 删除）时调用，
	// 回调在 rma 锁内执行，供上层清理该单的附属状态（如手续费欠额）。
	onExpire func(id RMAID)
}

func New(window, validFor int64) (*RMA, error) {
	if window < 1 || window > 1_000_000 || validFor < 1 || validFor > 1_000_000 {
		return nil, ErrInvalid
	}
	return &RMA{
		window: window,
		valid:  validFor,
		orders: make(map[OrderID]*orderRec),
		auths:  make(map[RMAID]*authRec),
	}, nil
}

func (r *RMA) AddOrder(order OrderID, shipAt int64, lines []Line, now int64) error {
	if order == "" || shipAt < 0 || shipAt > 1_000_000_000 ||
		now < 0 || now > 1_000_000_000 ||
		len(lines) < 1 || len(lines) > 100 {
		return ErrInvalid
	}
	seenLine := make(map[LineID]struct{}, len(lines))
	for _, ln := range lines {
		if ln.Shipped < 1 || ln.Shipped > 1_000_000 ||
			ln.Paid < 0 || ln.Paid > 1_000_000_000_000 {
			return ErrInvalid
		}
		if _, dup := seenLine[ln.ID]; dup {
			return ErrInvalid
		}
		seenLine[ln.ID] = struct{}{}
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if now < r.lastNow {
		return ErrClock
	}
	if _, ok := r.orders[order]; ok {
		return ErrConflict
	}

	om := &orderRec{shipAt: shipAt, lines: make(map[LineID]*lineRec, len(lines))}
	for _, ln := range lines {
		om.lines[ln.ID] = &lineRec{shipped: ln.Shipped, paid: ln.Paid}
	}
	r.orders[order] = om
	r.lastNow = now
	return nil
}

// ItemError 标记批量操作中最小失败项下标（从 0 起）。
type ItemError struct {
	Index int
	Err   error
}

func (e *ItemError) Error() string { return e.Err.Error() }
func (e *ItemError) Unwrap() error { return e.Err }

func (r *RMA) Authorize(id RMAID, order OrderID, items []Item, now int64) (exp int64, err error) {
	if id == "" || order == "" || len(items) < 1 || len(items) > 100 ||
		now < 0 || now > 1_000_000_000 {
		return 0, ErrInvalid
	}
	seen := make(map[LineID]struct{}, len(items))
	for _, it := range items {
		if it.Qty < 1 {
			return 0, ErrInvalid
		}
		if _, dup := seen[it.Line]; dup {
			return 0, ErrInvalid
		}
		seen[it.Line] = struct{}{}
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if now < r.lastNow {
		return 0, ErrClock
	}
	om, ok := r.orders[order]
	if !ok {
		return 0, ErrNotFound
	}
	for _, it := range items {
		if _, ok := om.lines[it.Line]; !ok {
			return 0, ErrNotFound
		}
	}
	if _, dup := r.auths[id]; dup {
		return 0, ErrConflict
	}
	if now-om.shipAt > r.window {
		return 0, ErrWindow
	}

	// 试落地到期：若随后余量校验拒绝则整体回滚，不留任何痕迹。
	p := r.collectExpired(now)

	for i, it := range items {
		ln := om.lines[it.Line]
		if ln.shipped-ln.qualified-ln.reserved < it.Qty {
			r.rollbackExpired(p)
			return 0, &ItemError{Index: i, Err: ErrCapacity}
		}
	}

	rec := &authRec{
		id:    id,
		order: order,
		exp:   now + r.valid,
		items: make(map[LineID]int64, len(items)),
	}
	for _, it := range items {
		rec.items[it.Line] = it.Qty
		om.lines[it.Line].reserved += it.Qty
	}
	r.auths[id] = rec
	heap.Push(&r.due, rec)
	r.commitExpired(p)
	r.touched++ // 新建的授权单记录
	r.lastNow = now
	return rec.exp, nil
}

// collectExpired 弹出并标记所有 exp <= now 的授权单（恰等即失效），
// 同时释放其未收数量；结果可用 rollbackExpired 撤销。
func (r *RMA) collectExpired(now int64) pendingExpire {
	var p pendingExpire
	for r.due.Len() > 0 && r.due[0].exp <= now {
		a := heap.Pop(&r.due).(*authRec)
		a.dead = true
		om := r.orders[a.order]
		for lid, qty := range a.items {
			if qty > 0 {
				ln := om.lines[lid]
				ln.reserved -= qty
				p.adjs = append(p.adjs, reservedAdj{ln, qty})
			}
		}
		p.expired = append(p.expired, a)
	}
	return p
}

func (r *RMA) rollbackExpired(p pendingExpire) {
	for _, x := range p.adjs {
		x.ln.reserved += x.qty
	}
	for i := len(p.expired) - 1; i >= 0; i-- {
		a := p.expired[i]
		a.dead = false
		heap.Push(&r.due, a)
	}
}

// commitExpired 彻底落账到期单：从 auths 删除。
func (r *RMA) commitExpired(p pendingExpire) {
	for _, a := range p.expired {
		delete(r.auths, a.id)
		if r.onExpire != nil {
			r.onExpire(a.id)
		}
	}
	r.touched += len(p.expired)
}

// TriedReceive 是 grade 包在 rma 锁内执行的收货回调。
// 全部校验通过后才调用；qualified=false 表示 C 级。
// 回调参数给出本次收货数量与所在订单行的金额/发货信息，供退款计算。
type TriedReceive func(qty int64, qualified bool, order OrderID, line LineID, paid, shipped int64) (refundDue, feeDeducted, paidOut int64)

func (r *RMA) Receive(id RMAID, line LineID, qty int64, now int64, qualified bool, settle TriedReceive) (refundDue, feeDeducted, paidOut int64, err error) {
	if id == "" || qty < 1 || now < 0 || now > 1_000_000_000 {
		return 0, 0, 0, ErrInvalid
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if now < r.lastNow {
		return 0, 0, 0, ErrClock
	}
	a, ok := r.auths[id]
	if !ok {
		return 0, 0, 0, ErrNotFound
	}
	om := r.orders[a.order]
	if _, inAuth := a.items[line]; !inAuth {
		return 0, 0, 0, ErrNotFound
	}
	ln := om.lines[line]

	p := r.collectExpired(now)
	if a.dead {
		r.rollbackExpired(p)
		return 0, 0, 0, ErrExpired
	}
	if a.items[line] < qty {
		r.rollbackExpired(p)
		return 0, 0, 0, ErrCapacity
	}

	// 全部校验通过：落地到期 → 锁内回调记账 → 扣减授权未收与余量。
	r.commitExpired(p)
	due, deducted, paid := settle(qty, qualified, a.order, line, ln.paid, ln.shipped)
	a.items[line] -= qty
	ln.reserved -= qty
	if qualified {
		ln.qualified += qty
	}
	r.lastNow = now
	return due, deducted, paid, nil
}

// Touched 与 ResetTouched 仅供测试验证性能论据。
func (r *RMA) Touched() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.touched
}

func (r *RMA) ResetTouched() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.touched = 0
}

// SetExpiryHook 注册到期清理回调，须在任何写操作之前调用一次。
func (r *RMA) SetExpiryHook(fn func(id RMAID)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.onExpire = fn
}
