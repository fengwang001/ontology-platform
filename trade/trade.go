package trade

import (
	"errors"
	"sort"
	"sync"

	"ontology/lot"
)

var (
	ErrInvalid   = errors.New("invalid argument")
	ErrClock     = errors.New("clock moved backwards")
	ErrUnknown   = errors.New("unknown account or symbol")
	ErrDuplicate = errors.New("duplicate symbol")
	ErrShortY    = errors.New("insufficient yesterday position")
	ErrShortT    = errors.New("insufficient today position")
	ErrShortPos  = errors.New("insufficient position")
	ErrNoFunds   = errors.New("insufficient funds")
)

// CloseMode 为平仓模式。
type CloseMode int

const (
	CloseYesterday CloseMode = iota
	CloseToday
	CloseAuto
)

// Symbol 为合约参数。
type Symbol struct {
	Mult int64
	MR   int64
	Fo   int64
	Fy   int64
	Ft   int64
}

// PosKey 为持仓维度。
type PosKey struct {
	Acct string
	Sym  string
	Dir  lot.Dir
}

// PosView 为单个仓位的结算只读视图。
type PosView struct {
	Key   PosKey
	QY    int64
	SP0   int64
	Today []lot.Batch
}

// Engine 为结算引擎。
type Engine struct {
	mu       sync.Mutex
	now      int64
	symbols  map[string]Symbol
	accounts map[string]int64
	pos      map[PosKey]*lot.Position
}

// NewEngine 创建引擎。
func NewEngine() *Engine {
	return &Engine{
		symbols:  make(map[string]Symbol),
		accounts: make(map[string]int64),
		pos:      make(map[PosKey]*lot.Position),
	}
}

// Now 返回当前时钟。
func (e *Engine) Now() int64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.now
}

func validNow(now int64) bool { return now >= 0 && now <= 1e12 }

func validBS(s []byte) bool { return len(s) > 0 }

func validRate(r int64) bool { return r >= 0 && r <= 10000 }

func validPrice(v int64) bool { return v >= 1 && v <= 1e7 }

func validQty(v int64) bool { return v >= 1 && v <= 1e6 }

func ceilDiv(a, b int64) int64 { // a >= 0, b > 0
	if a <= 0 {
		return 0
	}
	return (a + b - 1) / b
}

func validDir(d lot.Dir) bool { return d == lot.Long || d == lot.Short }

func validMode(m CloseMode) bool {
	return m == CloseYesterday || m == CloseToday || m == CloseAuto
}

// checkClock 仅校验时钟回退，不改变时钟。调用方须持锁。
func (e *Engine) checkClock(now int64) error {
	if now < e.now {
		return ErrClock
	}
	return nil
}

// commitClock 在全部校验通过后推进时钟。调用方须持锁。
func (e *Engine) commitClock(now int64) { e.now = now }

// AddSymbol 添加合约。
func (e *Engine) AddSymbol(now int64, sym []byte, mult, mr, fo, fy, ft int64) error {
	if !validNow(now) || !validBS(sym) || mult < 1 || mult > 1e4 ||
		!validRate(mr) || !validRate(fo) || !validRate(fy) || !validRate(ft) {
		return ErrInvalid
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.checkClock(now); err != nil {
		return err
	}
	name := string(sym)
	if _, ok := e.symbols[name]; ok {
		return ErrDuplicate
	}
	e.commitClock(now)
	e.symbols[name] = Symbol{Mult: mult, MR: mr, Fo: fo, Fy: fy, Ft: ft}
	return nil
}

// Deposit 入金并建立账户。
func (e *Engine) Deposit(now int64, acct []byte, amt int64) error {
	if !validNow(now) || !validBS(acct) || amt < 1 || amt > 1e12 {
		return ErrInvalid
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.checkClock(now); err != nil {
		return err
	}
	e.commitClock(now)
	e.accounts[string(acct)] += amt
	return nil
}

// marginLocked 计算账户全部持仓保证金之和（每个方向各取整一次）。调用方持锁。
func (e *Engine) marginLocked(acct string) int64 {
	var m int64
	for k, p := range e.pos {
		if k.Acct != acct {
			continue
		}
		sym := e.symbols[k.Sym]
		m += ceilDiv(p.Notional()*sym.Mult*sym.MR, 10000)
	}
	return m
}

// Open 开仓。
func (e *Engine) Open(now int64, acct, sym []byte, dir lot.Dir, price, qty int64) error {
	if !validNow(now) || !validBS(acct) || !validBS(sym) || !validDir(dir) ||
		!validPrice(price) || !validQty(qty) {
		return ErrInvalid
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.checkClock(now); err != nil {
		return err
	}
	sdef, ok := e.symbols[string(sym)]
	if !ok {
		return ErrUnknown
	}
	an := string(acct)
	bal, ok := e.accounts[an]
	if !ok {
		return ErrUnknown
	}
	k := PosKey{Acct: an, Sym: string(sym), Dir: dir}
	p := e.pos[k]
	if p == nil {
		p = lot.New()
		e.pos[k] = p
	}
	fee := ceilDiv(price*qty*sdef.Mult*sdef.Fo, 10000)
	notionalAfter := p.Notional() + price*qty
	marginNew := ceilDiv(notionalAfter*sdef.Mult*sdef.MR, 10000)
	marginTotal := e.marginLocked(an) - ceilDiv(p.Notional()*sdef.Mult*sdef.MR, 10000) + marginNew
	if bal-int64(fee) < marginTotal {
		return ErrNoFunds
	}
	e.commitClock(now)
	e.accounts[an] = bal - fee
	p.Open(price, qty)
	return nil
}

// CloseResult 为平仓结果。
type CloseResult struct {
	CloseY  int64
	CloseT  int64
	PnL     int64
	FeeY    int64
	FeeT    int64
	Touched int
}

// Close 平仓。
func (e *Engine) Close(now int64, acct, sym []byte, dir lot.Dir, price, qty int64, mode CloseMode) (CloseResult, error) {
	if !validNow(now) || !validBS(acct) || !validBS(sym) || !validDir(dir) ||
		!validPrice(price) || !validQty(qty) || !validMode(mode) {
		return CloseResult{}, ErrInvalid
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.checkClock(now); err != nil {
		return CloseResult{}, err
	}
	sdef, ok := e.symbols[string(sym)]
	if !ok {
		return CloseResult{}, ErrUnknown
	}
	an := string(acct)
	bal, ok := e.accounts[an]
	if !ok {
		return CloseResult{}, ErrUnknown
	}
	k := PosKey{Acct: an, Sym: string(sym), Dir: dir}
	p := e.pos[k]
	if p == nil {
		switch mode {
		case CloseYesterday:
			return CloseResult{}, ErrShortY
		case CloseToday:
			return CloseResult{}, ErrShortT
		default:
			return CloseResult{}, ErrShortPos
		}
	}

	// 先校验、后落账（全有或全无）。
	var yQty, tQty int64
	switch mode {
	case CloseYesterday:
		if !p.CanYesterday(qty) {
			return CloseResult{}, ErrShortY
		}
		yQty = qty
	case CloseToday:
		if !p.CanToday(qty) {
			return CloseResult{}, ErrShortT
		}
		tQty = qty
	case CloseAuto:
		if !p.CanAuto(qty) {
			return CloseResult{}, ErrShortPos
		}
		yQty = qty
		if yQty > p.QY() {
			yQty = p.QY()
		}
		tQty = qty - yQty
	}

	e.commitClock(now)
	var pnl int64
	sign := int64(dir)
	var touched int
	if yQty > 0 {
		pnl += sign * (price - p.SP0()) * yQty * sdef.Mult
		p.CloseYesterday(yQty)
	}
	if tQty > 0 {
		lots := p.CloseToday(tQty)
		touched = p.Touched()
		for _, l := range lots {
			pnl += sign * (price - l.OpenPrice) * l.Qty * sdef.Mult
		}
	}
	feeY := ceilDiv(price*yQty*sdef.Mult*sdef.Fy, 10000)
	feeT := ceilDiv(price*tQty*sdef.Mult*sdef.Ft, 10000)
	e.accounts[an] = bal + pnl - feeY - feeT
	return CloseResult{
		CloseY:  yQty,
		CloseT:  tQty,
		PnL:     pnl,
		FeeY:    feeY,
		FeeT:    feeT,
		Touched: touched,
	}, nil
}

// Balance 返回账户余额（不存在返回 0,false）。
func (e *Engine) Balance(acct []byte) (int64, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	b, ok := e.accounts[string(acct)]
	return b, ok
}

// Pos 返回仓位指针与是否存在（供 mtm 包使用）。
func (e *Engine) Pos(k PosKey) (*lot.Position, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	p, ok := e.pos[k]
	return p, ok
}

// PositionsOf 返回某合约全部仓位视图。
func (e *Engine) PositionsOf(sym []byte) []PosView {
	e.mu.Lock()
	defer e.mu.Unlock()
	sn := string(sym)
	views := make([]PosView, 0)
	for k, p := range e.pos {
		if k.Sym != sn {
			continue
		}
		v := PosView{Key: k, QY: p.QY(), SP0: p.SP0()}
		v.Today = append(v.Today, p.SnapshotToday()...)
		views = append(views, v)
	}
	return views
}

// ApplySettle 将某仓位盯市盈亏入账并合并今仓。
func (e *Engine) ApplySettle(k PosKey, pnl int64, sp int64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	p := e.pos[k]
	if p == nil {
		return
	}
	e.accounts[k.Acct] += pnl
	p.Merge(sp)
}

// Margin 返回账户全部持仓保证金之和。
func (e *Engine) Margin(acct []byte) int64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.marginLocked(string(acct))
}

// MarginCalls 返回余额严格小于保证金的账户（字节序）。
func (e *Engine) MarginCalls() [][]byte {
	e.mu.Lock()
	defer e.mu.Unlock()
	names := make([]string, 0)
	for a := range e.accounts {
		if e.accounts[a] < e.marginLocked(a) {
			names = append(names, a)
		}
	}
	sort.Strings(names)
	out := make([][]byte, len(names))
	for i, n := range names {
		out[i] = []byte(n)
	}
	return out
}

// HasSymbol / Symbol 供 mtm 做合约存在性与参数查询。
func (e *Engine) HasSymbol(sym []byte) (Symbol, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	s, ok := e.symbols[string(sym)]
	return s, ok
}

// BeginSettle 原子地完成结算前置校验（时钟 + 合约存在性）并推进时钟。
func (e *Engine) BeginSettle(now int64, sym []byte) (Symbol, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.checkClock(now); err != nil {
		return Symbol{}, err
	}
	sdef, ok := e.symbols[string(sym)]
	if !ok {
		return Symbol{}, ErrUnknown
	}
	e.commitClock(now)
	return sdef, nil
}
