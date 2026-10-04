// Package adjust 在统一引擎上编排持仓、公司行为、除权执行与在簿委托调价。
package adjust

import (
	"errors"
	"sync"

	"ontology/corpact"
	"ontology/holding"
)

var (
	ErrInvalidParam = errors.New("adjust: invalid parameter")
	ErrDateRollback = errors.New("adjust: date rollback")
	ErrDuplicateID  = errors.New("adjust: duplicate id")
	ErrNotFound     = errors.New("adjust: not found")
	ErrBadState     = errors.New("adjust: state mismatch")
	ErrConflict     = errors.New("adjust: conflict")
	ErrNoRefPrice   = errors.New("adjust: no reference close price")
	ErrInsufficient = errors.New("adjust: insufficient quantity")
)

const (
	maxDay   = 1_000_000
	maxCash  = 1_000_000_000_000
	maxDelta = 1_000_000_000
	maxPrice = 1_000_000_000
	maxDiv   = 1_000_000
	maxBonus = 100
	minPrice = 1
)

// Side 为委托方向。
type Side int

const (
	Buy Side = iota + 1
	Sell
)

type order struct {
	id    []byte
	acct  []byte
	sym   []byte
	side  Side
	price int64
	qty   int64
}

// Engine 是全部操作的串行化入口。
type Engine struct {
	mu   sync.RWMutex
	day  int64
	book *holding.Book
	reg  *corpact.Registry
	ords map[string]*order
}

func New() *Engine {
	return &Engine{
		book: holding.NewBook(),
		reg:  corpact.NewRegistry(),
		ords: map[string]*order{},
	}
}

func nonEmpty(b []byte) bool { return len(b) > 0 }

// Day 返回当前交易日。
func (e *Engine) Day() int64 {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.day
}

// Advance 推进当前交易日；不得回退，d 范围 [0,10^6]。
func (e *Engine) Advance(d int64) error {
	if d < 0 || d > maxDay {
		return ErrInvalidParam
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if d < e.day {
		return ErrDateRollback
	}
	if d == e.day {
		return nil
	}
	e.day = d
	e.runDue()
	return nil
}

// runDue 先做全部到期快照，再做全部到期执行；集合内按行动 id 字节序。
func (e *Engine) runDue() {
	for _, id := range e.reg.DueSnapshot(e.day) {
		a, err := e.reg.Get(id)
		if err != nil || a.State != corpact.Announced {
			continue
		}
		names, pos := e.book.SnapshotNamed(a.Sym)
		snap := make([]corpact.SnapEntry, 0, len(pos))
		for i, p := range pos {
			snap = append(snap, corpact.SnapEntry{
				Acct: []byte(names[i]),
				Q:    p.Q,
				F:    p.F,
			})
		}
		_ = e.reg.AttachSnap(id, snap)
	}
	for _, id := range e.reg.DueExecute(e.day) {
		e.execute(id)
	}
}

func (e *Engine) execute(id []byte) {
	a, err := e.reg.Get(id)
	if err != nil || a.State != corpact.Snapshotted {
		return
	}
	p, ok := e.book.Close(a.Sym)
	if !ok {
		p = minPrice
	}
	pex := refPrice(p, a.Cash, a.Bonus)

	names := make([]string, 0, len(a.Snap))
	pos := make([]holding.Pos, 0, len(a.Snap))
	for _, s := range a.Snap {
		if s.Q > 0 {
			names = append(names, string(s.Acct))
			pos = append(pos, holding.Pos{Q: s.Q, F: s.F})
		}
	}
	awards := e.book.Apply(a.Sym, names, pos, a.Cash, a.Bonus, pex)
	acctAwards := make([]corpact.AcctAward, 0, len(awards))
	for i, aw := range awards {
		acctAwards = append(acctAwards, corpact.AcctAward{
			Acct:       []byte(names[i]),
			Shares:     aw.Shares,
			SharesFroz: aw.SharesFroz,
			Cash:       aw.Cash,
			CashFroz:   aw.CashFroz,
			FragCash:   aw.FragCash,
		})
	}

	orderAdjs := e.adjustOrders(a.Sym, p, pex)
	e.book.SetClose(a.Sym, pex)
	_ = e.reg.AttachResult(id, &corpact.Result{
		Pex:    pex,
		Awards: acctAwards,
		Orders: orderAdjs,
	})
}

// adjustOrders 按 oid 字节序调价；新价 <1 的买单撤销并在清单中标明。
func (e *Engine) adjustOrders(sym []byte, p, pex int64) []corpact.OrderAdj {
	symStr := string(sym)
	var keys []string
	for k, o := range e.ords {
		if string(o.sym) == symStr {
			keys = append(keys, k)
		}
	}
	sortStrings(keys)
	adjs := make([]corpact.OrderAdj, 0, len(keys))
	for _, k := range keys {
		o := e.ords[k]
		num := o.price * pex
		var np int64
		if o.side == Buy {
			np = num / p
		} else {
			np = (num + p - 1) / p
		}
		if o.side == Buy && np < minPrice {
			delete(e.ords, k)
			adjs = append(adjs, corpact.OrderAdj{OID: append([]byte(nil), o.id...), NewPrice: 0, Canceled: true})
			continue
		}
		if np < minPrice {
			np = minPrice
		}
		o.price = np
		adjs = append(adjs, corpact.OrderAdj{OID: append([]byte(nil), o.id...), NewPrice: np})
	}
	return adjs
}

// refPrice 计算除权参考价：四舍五入（半分进位）到分，小于 1 取 1。
func refPrice(p, cash, bonus int64) int64 {
	num := 10*p - cash
	den := 10 + bonus
	q := (2*num + den) / (2 * den)
	if q < minPrice {
		return minPrice
	}
	return q
}

// Deposit 增加可用现金（cash 范围 [1,10^12]），账户不存在则建立。
func (e *Engine) Deposit(acct []byte, cash int64) error {
	if !nonEmpty(acct) || cash < 1 || cash > maxCash {
		return ErrInvalidParam
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.book.Deposit(acct, cash)
	return nil
}

// Trade 处理买卖后的持仓变动；delta 非零且 |delta|<=10^9。
// 账户在首次 Trade 时建立；卖出要求可用股数足够。
func (e *Engine) Trade(acct, sym []byte, delta int64) error {
	if !nonEmpty(acct) || !nonEmpty(sym) || delta == 0 ||
		delta > maxDelta || delta < -maxDelta {
		return ErrInvalidParam
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	selling := delta < 0
	if selling {
		if !e.book.HasAcct(acct) {
			return ErrNotFound
		}
	} else {
		e.book.EnsureAcct(acct)
	}
	if err := e.book.Trade(acct, sym, delta); err != nil {
		return mapHoldingErr(err)
	}
	return nil
}

// Freeze 将 n 股由可用转入冻结。
func (e *Engine) Freeze(acct, sym []byte, n int64) error {
	return e.moveFrozen(acct, sym, n, true)
}

// Unfreeze 将 n 股由冻结转回可用。
func (e *Engine) Unfreeze(acct, sym []byte, n int64) error {
	return e.moveFrozen(acct, sym, n, false)
}

func (e *Engine) moveFrozen(acct, sym []byte, n int64, freeze bool) error {
	if !nonEmpty(acct) || !nonEmpty(sym) || n < 1 {
		return ErrInvalidParam
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.book.HasAcct(acct) {
		return ErrNotFound
	}
	var err error
	if freeze {
		err = e.book.Freeze(acct, sym, n)
	} else {
		err = e.book.Unfreeze(acct, sym, n)
	}
	return mapHoldingErr(err)
}

// SetClose 记录标的收盘价（P 范围 [1,10^9]，单位分）。
func (e *Engine) SetClose(sym []byte, price int64) error {
	if !nonEmpty(sym) || price < 1 || price > maxPrice {
		return ErrInvalidParam
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.book.SetClose(sym, price)
	return nil
}

// PlaceOrder 只挂簿，不撮合也不锁券；oid 重复报 ErrDuplicateID。
func (e *Engine) PlaceOrder(oid, acct, sym []byte, side Side, price, qty int64) error {
	if !nonEmpty(oid) || !nonEmpty(acct) || !nonEmpty(sym) ||
		(side != Buy && side != Sell) || price < 1 || price > maxPrice || qty < 1 || qty > maxDelta {
		return ErrInvalidParam
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.ords[string(oid)]; ok {
		return ErrDuplicateID
	}
	e.ords[string(oid)] = &order{
		id:    append([]byte(nil), oid...),
		acct:  append([]byte(nil), acct...),
		sym:   append([]byte(nil), sym...),
		side:  side,
		price: price,
		qty:   qty,
	}
	return nil
}

// CancelOrder 撤销在簿委托；不存在报 ErrNotFound。
func (e *Engine) CancelOrder(oid []byte) error {
	if !nonEmpty(oid) {
		return ErrInvalidParam
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.ords[string(oid)]; !ok {
		return ErrNotFound
	}
	delete(e.ords, string(oid))
	return nil
}

// Announce 登记公司行为。
// 校验次序：参数非法 > 编号重复 > 状态不符(rec 太早) > 冲突 > 无参考价。
func (e *Engine) Announce(id, sym []byte, cash, bonus, rec, ex int64) error {
	if !nonEmpty(id) || !nonEmpty(sym) ||
		cash < 0 || cash > maxDiv || bonus < 0 || bonus > maxBonus ||
		(cash == 0 && bonus == 0) ||
		rec < 0 || ex < 0 || rec > maxDay || ex > maxDay || rec >= ex {
		return ErrInvalidParam
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, err := e.reg.Get(id); err == nil {
		return ErrDuplicateID
	}
	if rec > e.day {
		return ErrBadState
	}
	if e.reg.HasPending(sym) {
		return ErrConflict
	}
	if !e.book.HasClose(sym) {
		return ErrNoRefPrice
	}
	if err := e.reg.Announce(id, sym, cash, bonus, rec, ex); err != nil {
		return mapCorpactErr(err)
	}
	return nil
}

// CancelAction 仅在快照生成之前允许撤销。
func (e *Engine) CancelAction(id []byte) error {
	if !nonEmpty(id) {
		return ErrInvalidParam
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, err := e.reg.Get(id); err != nil {
		return ErrNotFound
	}
	return mapCorpactErr(e.reg.Cancel(id))
}

// Result 返回除权执行结果；未登记报 ErrNotFound，未执行报 ErrBadState。
func (e *Engine) Result(id []byte) (*corpact.Result, error) {
	if !nonEmpty(id) {
		return nil, ErrInvalidParam
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	a, err := e.reg.Get(id)
	if err != nil {
		return nil, ErrNotFound
	}
	if a.State != corpact.Executed || a.Result == nil {
		return nil, ErrBadState
	}
	return a.Result, nil
}

func mapHoldingErr(err error) error {
	switch {
	case errors.Is(err, holding.ErrNoAccount):
		return ErrNotFound
	case errors.Is(err, holding.ErrInsufficient):
		return ErrInsufficient
	default:
		return err
	}
}

func mapCorpactErr(err error) error {
	switch {
	case errors.Is(err, corpact.ErrDuplicateID):
		return ErrDuplicateID
	case errors.Is(err, corpact.ErrNotFound):
		return ErrNotFound
	case errors.Is(err, corpact.ErrBadState):
		return ErrBadState
	case errors.Is(err, corpact.ErrConflict):
		return ErrConflict
	case errors.Is(err, corpact.ErrNoRefPrice):
		return ErrNoRefPrice
	default:
		return err
	}
}

func sortStrings(x []string) {
	for i := 1; i < len(x); i++ {
		for j := i; j > 0 && x[j-1] > x[j]; j-- {
			x[j-1], x[j] = x[j], x[j-1]
		}
	}
}
