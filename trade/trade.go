// Package trade 提供期货开平仓成交、手续费与逐日盯市结算的引擎。
package trade

import (
	"bytes"
	"errors"
	"math/big"
	"sort"
	"sync"

	"ontology/lot"
	"ontology/mtm"
)

var (
	ErrInvalidParam      = errors.New("trade: 参数非法")
	ErrClock             = errors.New("trade: 时钟回退")
	ErrAccountNotFound   = errors.New("trade: 账户不存在")
	ErrSymbolNotFound    = errors.New("trade: 合约不存在")
	ErrSymbolDuplicate   = errors.New("trade: 合约重复")
	ErrInsufficientFunds = errors.New("trade: 资金不足")
)

// CloseMode 为平仓模式：平昨、平今或自动（先昨后今）。
type CloseMode int

const (
	CloseYesterday CloseMode = iota
	CloseToday
	CloseAuto
)

// Valid 报告平仓模式是否合法。
func (m CloseMode) Valid() bool {
	return m == CloseYesterday || m == CloseToday || m == CloseAuto
}

// Engine 为结算引擎，所有方法可并发调用，等价于某个串行顺序。
type Engine struct {
	mu    sync.Mutex
	now   int64
	accts map[string]int64
	syms  map[string]mtm.Contract
	book  *lot.Book
}

// New 创建空引擎。
func New() *Engine {
	return &Engine{
		accts: make(map[string]int64),
		syms:  make(map[string]mtm.Contract),
		book:  lot.NewBook(),
	}
}

// AddSymbol 添加合约，重复添加报 ErrSymbolDuplicate。
func (e *Engine) AddSymbol(now int64, sym []byte, mult, mr, fo, fy, ft int64) error {
	if !validNow(now) || len(sym) == 0 ||
		mult < 1 || mult > 1e4 ||
		mr < 0 || mr > 10000 || fo < 0 || fo > 10000 ||
		fy < 0 || fy > 10000 || ft < 0 || ft > 10000 {
		return ErrInvalidParam
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if now < e.now {
		return ErrClock
	}
	s := string(sym)
	if _, ok := e.syms[s]; ok {
		return ErrSymbolDuplicate
	}
	e.syms[s] = mtm.Contract{Mult: mult, Mr: mr, Fo: fo, Fy: fy, Ft: ft}
	e.now = now
	return nil
}

// Deposit 入金并建立账户。
func (e *Engine) Deposit(now int64, acct []byte, amt int64) error {
	if !validNow(now) || len(acct) == 0 || amt < 1 || amt > 1e12 {
		return ErrInvalidParam
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if now < e.now {
		return ErrClock
	}
	e.accts[string(acct)] += amt
	e.now = now
	return nil
}

// Open 开仓，手续费即时扣除，要求余额减手续费后不小于开仓后的全部保证金。
func (e *Engine) Open(now int64, acct, sym []byte, dir lot.Direction, price, qty int64) error {
	if !validNow(now) || len(acct) == 0 || len(sym) == 0 ||
		!dir.Valid() || !validPrice(price) || !validQty(qty) {
		return ErrInvalidParam
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if now < e.now {
		return ErrClock
	}
	acctStr, symStr := string(acct), string(sym)
	bal, ok := e.accts[acctStr]
	if !ok {
		return ErrAccountNotFound
	}
	c, ok := e.syms[symStr]
	if !ok {
		return ErrSymbolNotFound
	}
	fee := mtm.CeilRate(price*qty*c.Mult, c.Fo)
	key := lot.Key{Acct: acctStr, Sym: symStr, Dir: dir}
	after := lot.Position{Today: []lot.Batch{{Price: price, Qty: qty}}}
	if pos := e.book.Get(key); pos != nil {
		after = *pos
		after.Today = make([]lot.Batch, len(pos.Today)+1)
		copy(after.Today, pos.Today)
		after.Today[len(pos.Today)] = lot.Batch{Price: price, Qty: qty}
	}
	total := new(big.Int)
	found := false
	e.book.Each(func(k lot.Key, p *lot.Position) {
		if k.Acct != acctStr {
			return
		}
		if k == key {
			found = true
			p = &after
		}
		total.Add(total, mtm.Margin(p, e.syms[k.Sym]))
	})
	if !found {
		total.Add(total, mtm.Margin(&after, c))
	}
	if new(big.Int).Sub(big.NewInt(bal), big.NewInt(fee)).Cmp(total) < 0 {
		return ErrInsufficientFunds
	}
	e.book.Ensure(key).Open(price, qty)
	e.accts[acctStr] -= fee
	e.now = now
	return nil
}

// Close 平仓，盈亏与手续费即时计入余额，不做资金检查。
func (e *Engine) Close(now int64, acct, sym []byte, dir lot.Direction, price, qty int64, mode CloseMode) error {
	if !validNow(now) || len(acct) == 0 || len(sym) == 0 ||
		!dir.Valid() || !validPrice(price) || !validQty(qty) || !mode.Valid() {
		return ErrInvalidParam
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if now < e.now {
		return ErrClock
	}
	acctStr, symStr := string(acct), string(sym)
	if _, ok := e.accts[acctStr]; !ok {
		return ErrAccountNotFound
	}
	c, ok := e.syms[symStr]
	if !ok {
		return ErrSymbolNotFound
	}
	pos := e.book.Get(lot.Key{Acct: acctStr, Sym: symStr, Dir: dir})
	if pos == nil {
		pos = &lot.Position{}
	}
	s := dir.Sign()
	var pnl, fee int64
	switch mode {
	case CloseYesterday:
		if err := pos.CloseYesterday(qty); err != nil {
			return err
		}
		pnl = s * (price - pos.Sp0) * qty * c.Mult
		fee = mtm.CeilRate(price*qty*c.Mult, c.Fy)
	case CloseToday:
		batches, err := pos.CloseToday(qty)
		if err != nil {
			return err
		}
		pnl = s * todayUnits(price, batches) * c.Mult
		fee = mtm.CeilRate(price*qty*c.Mult, c.Ft)
	case CloseAuto:
		yQty, batches, err := pos.CloseAuto(qty)
		if err != nil {
			return err
		}
		units := (price-pos.Sp0)*yQty + todayUnits(price, batches)
		pnl = s * units * c.Mult
		fee = mtm.CeilRate(price*yQty*c.Mult, c.Fy) +
			mtm.CeilRate(price*(qty-yQty)*c.Mult, c.Ft)
	}
	e.accts[acctStr] += pnl - fee
	e.now = now
	return nil
}

// Settle 对该合约全部持仓计盯市盈亏并入余额，返回追保名单（按账户字节序）。
func (e *Engine) Settle(now int64, sym []byte, sp int64) ([][]byte, error) {
	if !validNow(now) || len(sym) == 0 || !validPrice(sp) {
		return nil, ErrInvalidParam
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if now < e.now {
		return nil, ErrClock
	}
	symStr := string(sym)
	c, ok := e.syms[symStr]
	if !ok {
		return nil, ErrSymbolNotFound
	}
	e.book.Each(func(k lot.Key, p *lot.Position) {
		if k.Sym != symStr {
			return
		}
		e.accts[k.Acct] += mtm.SettlePnL(p, k.Dir, sp, c)
		p.Absorb(sp)
	})
	var calls [][]byte
	for acct, bal := range e.accts {
		if big.NewInt(bal).Cmp(e.totalMargin(acct)) < 0 {
			calls = append(calls, []byte(acct))
		}
	}
	sort.Slice(calls, func(i, j int) bool { return bytes.Compare(calls[i], calls[j]) < 0 })
	e.now = now
	return calls, nil
}

// totalMargin 汇总该账户全部持仓的保证金（各持仓已分别取整）。
func (e *Engine) totalMargin(acct string) *big.Int {
	total := new(big.Int)
	e.book.Each(func(k lot.Key, p *lot.Position) {
		if k.Acct == acct {
			total.Add(total, mtm.Margin(p, e.syms[k.Sym]))
		}
	})
	return total
}

// todayUnits 返回被平今仓各批的 (平仓价-开仓价)*手数 之和。
func todayUnits(price int64, batches []lot.Batch) int64 {
	var units int64
	for _, b := range batches {
		units += (price - b.Price) * b.Qty
	}
	return units
}

func validNow(now int64) bool     { return now >= 0 && now <= 1e12 }
func validPrice(price int64) bool { return price >= 1 && price <= 1e7 }
func validQty(qty int64) bool     { return qty >= 1 && qty <= 1e6 }

// Balance 返回账户余额与账户是否存在。
func (e *Engine) Balance(acct []byte) (int64, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	bal, ok := e.accts[string(acct)]
	return bal, ok
}
