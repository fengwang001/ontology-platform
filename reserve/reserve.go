// Package reserve 维护波次拣货的预占账（订单 × 库位 → 未拣量）。
package reserve

import (
	"sort"

	"ontology/slot"
)

// Err 哨兵错误复用 slot 包，保证五类错误 errors.Is 一致。
var (
	ErrInvalidArg  = slot.ErrInvalidArg
	ErrNotFound    = slot.ErrNotFound
	ErrBadState    = slot.ErrBadState
	ErrQtyMismatch = slot.ErrQtyMismatch
	ErrConflict    = slot.ErrConflict
)

// Rec 为一条预占记录的只读视图。
type Rec struct {
	Order string
	Loc   string
	Qty   int64
}

// Ledger 是预占账；自身不加锁，由上层 wave.Coordinator 串行化。
type Ledger struct {
	recs  map[key]int64
	byLoc map[string]map[string]int64 // loc → order → qty
}

type key struct {
	order string
	loc   string
}

// NewLedger 创建空账本。
func NewLedger() *Ledger {
	return &Ledger{
		recs:  map[key]int64{},
		byLoc: map[string]map[string]int64{},
	}
}

// Add 合并同一（订单,库位）的多次取货。
func (l *Ledger) Add(order, loc string, qty int64) {
	if qty <= 0 {
		return
	}
	k := key{order, loc}
	l.recs[k] += qty
	at := l.byLoc[loc]
	if at == nil {
		at = map[string]int64{}
		l.byLoc[loc] = at
	}
	at[order] += qty
}

// Qty 返回某条预占记录的未拣量。
func (l *Ledger) Qty(order, loc string) (int64, bool) {
	q, ok := l.recs[key{order, loc}]
	return q, ok
}

// Delete 删除一条记录。
func (l *Ledger) Delete(order, loc string) {
	delete(l.recs, key{order, loc})
	if at, ok := l.byLoc[loc]; ok {
		delete(at, order)
		if len(at) == 0 {
			delete(l.byLoc, loc)
		}
	}
}

// DropLoc 删除某库位上全部预占记录，返回受影响（订单, 数量），按订单号字节序。
func (l *Ledger) DropLoc(loc string) []Rec {
	at := l.byLoc[loc]
	out := make([]Rec, 0, len(at))
	for order, qty := range at {
		out = append(out, Rec{Order: order, Loc: loc, Qty: qty})
		delete(l.recs, key{order, loc})
	}
	delete(l.byLoc, loc)
	sort.Slice(out, func(i, j int) bool { return out[i].Order < out[j].Order })
	return out
}

// LocOrders 返回某库位上的记录数。
func (l *Ledger) LocCount(loc string) int { return len(l.byLoc[loc]) }

// OrderLocs 返回某订单全部预占，按库位编号字节序。
func (l *Ledger) OrderLocs(order string) []Rec {
	out := []Rec{}
	for k, qty := range l.recs {
		if k.order == order {
			out = append(out, Rec{Order: order, Loc: k.loc, Qty: qty})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Loc < out[j].Loc })
	return out
}

// OrderTotal 返回某订单全部未拣预占之和。
func (l *Ledger) OrderTotal(order string) int64 {
	var n int64
	for k, qty := range l.recs {
		if k.order == order {
			n += qty
		}
	}
	return n
}

// Total 返回账本全部未拣量之和（不变量核对用）。
func (l *Ledger) Total() int64 {
	var n int64
	for _, qty := range l.recs {
		n += qty
	}
	return n
}
