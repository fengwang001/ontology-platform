// Package gate 为委托前置检查与成交/撤单回写的风控网关。
package gate

import (
	"errors"
	"sync"

	"ontology/exposure"
	"ontology/limit"
)

// Side / Offset 直接复用 limit 包枚举。
type (
	Side   = limit.Side
	Offset = limit.Offset
)

const (
	Long  = limit.Long
	Short = limit.Short
	Open  = limit.Open
	Close = limit.Close
)

// 哨兵错误，按拒绝次序排列；可用 errors.Is 区分。
var (
	ErrInvalid      = errors.New("invalid argument")
	ErrClock        = errors.New("clock moved backwards")
	ErrNotFound     = errors.New("account, symbol or order not found")
	ErrDuplicate    = limit.ErrDuplicate // 账户重复登记
	ErrDuplicateOID = errors.New("duplicate order id")
	ErrState        = errors.New("order already terminated or fill exceeds remaining")
	ErrCloseQty     = errors.New("close quantity exceeds closeable position")
	ErrAcctLimit    = errors.New("account side limit exceeded")
	ErrGroupLimit   = errors.New("group side limit exceeded")
	ErrDayLimit     = errors.New("intraday open volume limit exceeded")
)

// Order 为委托记录。
type Order struct {
	Now      int64
	OID      []byte
	Acct     []byte
	Sym      []byte
	Side     Side
	Offset   Offset
	Qty      int64
	Remain   int64
	Canceled bool
}

// Gateway 为风控前置网关，并发安全。
type Gateway struct {
	mu   sync.RWMutex
	now  int64
	reg  *limit.Registry
	book *exposure.Book
	ords map[string]*Order
}

// New 创建空网关。
func New() *Gateway {
	return &Gateway{reg: limit.NewRegistry(), book: exposure.NewBook(), ords: map[string]*Order{}}
}

// Register 把账户固定归入一个组。
func (g *Gateway) Register(now int64, acct, group []byte) error {
	if !validNow(now) || !validID(acct) || !validID(group) {
		return ErrInvalid
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if now < g.now {
		return ErrClock
	}
	if err := g.reg.Register(now, acct, group); err != nil {
		if errors.Is(err, limit.ErrDuplicate) {
			return ErrDuplicate
		}
		return err
	}
	g.now = now
	return nil
}

// SetLimit 设置合约限额（始终接受，含调低）。
func (g *Gateway) SetLimit(now int64, sym []byte, acctLim, groupLim, dayOpen int64) {
	if !validNow(now) || !validID(sym) || !validCap(acctLim) || !validCap(groupLim) || !validCap(dayOpen) {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if now < g.now {
		return
	}
	g.reg.SetLimit(now, sym, acctLim, groupLim, dayOpen)
	g.now = now
}

// SetHedge 设置账户合约某边套保额度（始终接受）。
func (g *Gateway) SetHedge(now int64, acct, sym []byte, side Side, hedge int64) error {
	if !validNow(now) || !validID(acct) || !validID(sym) || !validSide(side) || !validCap(hedge) {
		return ErrInvalid
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if now < g.now {
		return ErrClock
	}
	if _, ok := g.reg.Group(acct); !ok {
		return ErrNotFound
	}
	if _, ok := g.reg.Limits(sym); !ok {
		return ErrNotFound
	}
	group, _ := g.reg.Group(acct)
	g.book.SetHedge(acct, group, sym, side, hedge)
	g.now = now
	return nil
}

// Order 提交一笔委托；返回拒绝原因（nil 表示放行）。
func (g *Gateway) Order(now int64, oid, acct, sym []byte, side Side, offset Offset, qty int64) error {
	if !validNow(now) || !validID(oid) || !validID(acct) || !validID(sym) ||
		!validSide(side) || !validOffset(offset) || !validQty(qty) {
		return ErrInvalid
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if now < g.now {
		return ErrClock
	}
	// 不存在：账户未登记 / 合约未 SetLimit（先于 oid 重复）。
	group, ok := g.reg.Group(acct)
	if !ok {
		return ErrNotFound
	}
	lim, ok := g.reg.Limits(sym)
	if !ok {
		return ErrNotFound
	}
	if old, dup := g.ords[string(oid)]; dup {
		if old.terminated() {
			return ErrState
		}
		return ErrDuplicateOID
	}
	if offset == Close {
		if qty > g.book.Closeable(acct, sym, side) {
			return ErrCloseQty
		}
		g.book.AcceptClose(acct, group, sym, side, qty)
	} else {
		switch g.book.CheckOpen(acct, group, sym, side, qty, lim.Acct, lim.Group, lim.DayOpen) {
		case exposure.AcctLimit:
			return ErrAcctLimit
		case exposure.GroupLimit:
			return ErrGroupLimit
		case exposure.DayLimit:
			return ErrDayLimit
		}
		g.book.AcceptOpen(acct, group, sym, side, qty)
	}
	g.ords[string(oid)] = &Order{
		Now: now, OID: append([]byte(nil), oid...), Acct: append([]byte(nil), acct...),
		Sym: append([]byte(nil), sym...), Side: side, Offset: offset, Qty: qty, Remain: qty,
	}
	g.now = now
	return nil
}

// Fill 对委托部分或全部成交。
func (g *Gateway) Fill(now int64, oid []byte, qty int64) error {
	if !validNow(now) || !validID(oid) || !validQty(qty) {
		return ErrInvalid
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if now < g.now {
		return ErrClock
	}
	o, ok := g.ords[string(oid)]
	if !ok {
		return ErrNotFound
	}
	if o.terminated() {
		return ErrState
	}
	if qty > o.Remain {
		return ErrState
	}
	group, _ := g.reg.Group(o.Acct)
	if o.Offset == Open {
		g.book.FillOpen(o.Acct, group, o.Sym, o.Side, qty)
	} else {
		g.book.FillClose(o.Acct, group, o.Sym, o.Side, qty)
	}
	o.Remain -= qty
	g.now = now
	return nil
}

// Cancel 撤销委托未成交部分。
func (g *Gateway) Cancel(now int64, oid []byte) error {
	if !validNow(now) || !validID(oid) {
		return ErrInvalid
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if now < g.now {
		return ErrClock
	}
	o, ok := g.ords[string(oid)]
	if !ok {
		return ErrNotFound
	}
	if o.terminated() {
		return ErrState
	}
	group, _ := g.reg.Group(o.Acct)
	if o.Offset == Open {
		g.book.CancelOpen(o.Acct, group, o.Sym, o.Side, o.Remain)
	} else {
		g.book.CancelClose(o.Acct, group, o.Sym, o.Side, o.Remain)
	}
	o.Remain = 0
	o.Canceled = true
	g.now = now
	return nil
}

// ResetDay 切换交易日：清零已成交开仓部分，在途保留。
func (g *Gateway) ResetDay(now int64) {
	if !validNow(now) {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if now < g.now {
		return
	}
	g.book.ResetDay()
	g.now = now
}

// Touched 返回最近一次逐笔变更触碰的账户记录数（测试用）。
func (g *Gateway) Touched() int {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.book.Touched()
}

// AcctRec 返回账户在某合约上的双边状态快照（测试/可观测用）。
func (g *Gateway) AcctRec(acct, sym []byte) exposure.Rec {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.book.Rec(acct, sym)
}

// AcctExposure 返回账户某合约某边敞口 e=持仓+在途开仓。
func (g *Gateway) AcctExposure(acct, sym []byte, side Side) int64 {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.book.AcctExp(acct, sym, side)
}

// GroupExposure 返回某组某合约的组敞口。
func (g *Gateway) GroupExposure(group, sym []byte) int64 {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.book.GroupExp(group, sym)
}

// DayOpen 返回账户某合约日内开仓量。
func (g *Gateway) DayOpen(acct, sym []byte) int64 {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.book.DayOpen(acct, sym)
}

// LookupOrder 返回委托副本及是否存在。
func (g *Gateway) LookupOrder(oid []byte) (Order, bool) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	o, ok := g.ords[string(oid)]
	if !ok {
		return Order{}, false
	}
	return *o, true
}

func (o *Order) terminated() bool { return o.Canceled || o.Remain == 0 }

func validNow(now int64) bool   { return now >= 0 && now <= 1_000_000_000_000 }
func validID(id []byte) bool    { return len(id) > 0 }
func validQty(q int64) bool     { return q >= 1 && q <= 1_000_000_000 }
func validCap(c int64) bool     { return c >= 0 && c <= 1_000_000_000 }
func validSide(s Side) bool     { return s == Long || s == Short }
func validOffset(o Offset) bool { return o == Open || o == Close }
