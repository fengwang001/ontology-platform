// Package adjust 提供除权处理器门面 Engine：串行化全部操作，
// 在除权日执行送股、现金红利、碎股折现，并对在簿委托调价。
package adjust

import (
	"sort"
	"sync"

	"ontology/corpact"
	"ontology/holding"
)

// 哨兵错误再导出，供 errors.Is 使用。
var (
	ErrInvalidParam = holding.ErrInvalidParam
	ErrDateRollback = holding.ErrDateRollback
	ErrDuplicate    = holding.ErrDuplicate
	ErrNotExist     = holding.ErrNotExist
	ErrState        = holding.ErrState
	ErrConflict     = holding.ErrConflict
	ErrNoRefPrice   = holding.ErrNoRefPrice
	ErrInsufficient = holding.ErrInsufficient
)

const (
	// MaxPrice 为委托价格上限（分，含）。
	MaxPrice = int64(1_000_000_000)
	// MaxQty 为委托数量上限（含）。
	MaxQty = int64(1_000_000_000)
)

// Side 为委托方向。
type Side int

const (
	// Buy 买单。
	Buy Side = iota
	// Sell 卖单。
	Sell
)

// Order 为在簿委托。
type Order struct {
	Oid   string
	Acct  string
	Sym   string
	Side  Side
	Price int64
	Qty   int64
}

// Engine 为除权处理器门面；所有公开方法可并发调用，
// 效果等价于某个串行顺序。
type Engine struct {
	mu     sync.Mutex
	book   *holding.Book
	reg    *corpact.Registry
	orders map[string]*Order

	touched int // 一次除权执行触碰的账户记录数（非导出，测试佐证用）
}

// New 创建空处理器。
func New() *Engine {
	book := holding.NewBook()
	return &Engine{
		book:   book,
		reg:    corpact.New(book),
		orders: make(map[string]*Order),
	}
}

// Day 返回当前交易日。
func (e *Engine) Day() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.reg.Day()
}

// Advance 推进交易日到 d；先为越过的登记日拍快照，再执行到期的除权。
func (e *Engine) Advance(d int) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	execIDs, err := e.reg.Advance(d)
	if err != nil {
		return err
	}
	for _, id := range execIDs {
		act, ok := e.reg.GetAction(id)
		if ok {
			e.execute(act)
		}
	}
	return nil
}

// Deposit 增加可用现金。
func (e *Engine) Deposit(acct string, cash int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.book.Deposit(acct, cash)
}

// Trade 记一笔成交，负数为卖出。
func (e *Engine) Trade(acct, sym string, delta int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.book.Trade(acct, sym, delta)
}

// Freeze 冻结 n 股。
func (e *Engine) Freeze(acct, sym string, n int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.book.Freeze(acct, sym, n)
}

// Unfreeze 解冻 n 股。
func (e *Engine) Unfreeze(acct, sym string, n int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.book.Unfreeze(acct, sym, n)
}

// SetClose 记收盘价。
func (e *Engine) SetClose(sym string, p int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.reg.SetClose(sym, p)
}

// Announce 登记公司行为。
func (e *Engine) Announce(id, sym string, c, b int64, rec, ex int) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.reg.Announce(id, sym, c, b, rec, ex)
}

// CancelAction 撤销登记（仅快照之前）。
func (e *Engine) CancelAction(id string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.reg.CancelAction(id)
}

// Result 返回已执行行动的除权结果。
func (e *Engine) Result(id string) (*corpact.Result, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.reg.Result(id)
}

// PlaceOrder 挂在簿委托，不撮合也不锁券。
func (e *Engine) PlaceOrder(oid, acct, sym string, side Side, price, qty int64) error {
	if oid == "" || acct == "" || sym == "" ||
		(side != Buy && side != Sell) ||
		price < 1 || price > MaxPrice || qty < 1 || qty > MaxQty {
		return ErrInvalidParam
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.orders[oid]; ok {
		return ErrDuplicate
	}
	e.orders[oid] = &Order{Oid: oid, Acct: acct, Sym: sym, Side: side, Price: price, Qty: qty}
	return nil
}

// CancelOrder 撤销在簿委托。
func (e *Engine) CancelOrder(oid string) error {
	if oid == "" {
		return ErrInvalidParam
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.orders[oid]; !ok {
		return ErrNotExist
	}
	delete(e.orders, oid)
	return nil
}

// GetOrder 返回在簿委托；不存在时 ok=false。
func (e *Engine) GetOrder(oid string) (Order, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	o, ok := e.orders[oid]
	if !ok {
		return Order{}, false
	}
	return *o, true
}

// Position 返回账户某标的的总持仓与冻结。
func (e *Engine) Position(acct, sym string) (q, f int64, ok bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.book.Position(acct, sym)
}

// Cash 返回账户可用与冻结现金。
func (e *Engine) Cash(acct string) (avail, frozen int64, ok bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.book.Cash(acct)
}

// computePex 计算除权参考价：(10*P-c)/(10+b) 四舍五入到分
// （恰为半分时进位），结果小于 1 取 1。全程整数算术。
func computePex(p, c, b int64) int64 {
	num := 10*p - c
	den := 10 + b
	pex := (2*num + den) / (2 * den)
	if pex < 1 {
		pex = 1
	}
	return pex
}

// execute 执行一次除权：先按账户字节序入账，再按 oid 字节序调价。
func (e *Engine) execute(act *corpact.Action) {
	p, _ := e.reg.Close(act.Sym)
	pex := computePex(p, act.C, act.B)
	res := &corpact.Result{Pex: pex}

	accts := make([]string, 0, len(act.Snapshot))
	for acct := range act.Snapshot {
		accts = append(accts, acct)
	}
	sort.Strings(accts)
	for _, acct := range accts {
		s := act.Snapshot[acct]
		n := s.Q * act.B / 10
		nf := s.F * act.B / 10
		m := s.Q * act.C / 10
		mf := s.F * act.C / 10
		frac := (s.Q * act.B % 10) * pex / 10
		e.book.Credit(acct, act.Sym, n, nf, m-mf+frac, mf)
		e.touched++
		res.Gains = append(res.Gains, corpact.AccountGain{
			Acct:         acct,
			Shares:       n,
			FrozenShares: nf,
			Cash:         m,
			FrozenCash:   mf,
			FractionCash: frac,
		})
	}

	oids := make([]string, 0, len(e.orders))
	for oid, o := range e.orders {
		if o.Sym == act.Sym {
			oids = append(oids, oid)
		}
	}
	sort.Strings(oids)
	for _, oid := range oids {
		o := e.orders[oid]
		adj := corpact.OrderAdj{Oid: oid, OldPrice: o.Price}
		if o.Side == Buy {
			np := o.Price * pex / p
			adj.NewPrice = np
			if np < 1 {
				adj.Cancelled = true
				delete(e.orders, oid)
			} else {
				o.Price = np
			}
		} else {
			np := (o.Price*pex + p - 1) / p
			adj.NewPrice = np
			o.Price = np
		}
		res.Orders = append(res.Orders, adj)
	}

	e.reg.RecordResult(act.ID, res)
}
