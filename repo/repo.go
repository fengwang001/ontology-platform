// Package repo 提供质押式回购标准券质押库引擎：日期推进、入口结算、
// 融资回购、到期购回与违约处置。所有操作在互斥锁下串行执行，
// 并发调用结果等价于某个串行顺序。
package repo

import (
	"sort"
	"sync"

	"ontology/haircut"
	"ontology/pledge"
)

// 错误哨兵，与 haircut 包同源，可用 errors.Is 区分。
var (
	ErrParam             = haircut.ErrParam
	ErrDayRollback       = haircut.ErrDayRollback
	ErrNotExist          = haircut.ErrNotExist
	ErrDuplicate         = haircut.ErrDuplicate
	ErrDeficit           = haircut.ErrDeficit
	ErrInsufficientAvail = haircut.ErrInsufficientAvail
	ErrInsufficientStock = haircut.ErrInsufficientStock
	ErrInsufficientCap   = haircut.ErrInsufficientCap
)

// Status 为回购状态。
type Status int

const (
	Outstanding Status = iota // 未了结
	Redeemed                  // 已购回
	Defaulted                 // 已违约
)

// Info 为回购的可观测信息。
type Info struct {
	Status  Status
	Amount  int64
	Repay   int64 // 到期应还
	Due     int64 // 到期日
	BadDebt int64 // 违约处置后仍未偿清的坏账
}

// Deficit 为欠库账户及其缺口 Use-Cap。
type Deficit struct {
	Acct []byte
	Gap  int64
}

type repoRec struct {
	acct    string
	amount  int64
	repay   int64
	due     int64
	use     int64
	seq     int64 // 被接受序号
	status  Status
	badDebt int64
}

// Engine 为质押库引擎。零值不可用，须用 New 构造。
type Engine struct {
	mu    sync.Mutex
	day   int64 // 已接受的最大 day
	seq   int64 // 已接受回购的序号计数
	bonds *haircut.Table
	lib   *pledge.Library
	repos map[string]*repoRec
}

func New() *Engine {
	return &Engine{
		bonds: haircut.NewTable(),
		lib:   pledge.NewLibrary(),
		repos: make(map[string]*repoRec),
	}
}

func ceilDiv(a, b int64) int64 { return (a + b - 1) / b }

func validDay(day int64) bool { return day >= 0 && day <= 1_000_000 }

func validQty(n int64) bool { return n >= 1 && n <= 1_000_000_000_000 }

// advance 在参数与日期检查通过后推进日期并做入口结算；
// 结算与日期推进不因其后的业务拒绝而回滚。
func (e *Engine) advance(day int64) error {
	if day < e.day {
		return ErrDayRollback
	}
	e.day = day
	e.settle()
	return nil
}

// settle 把到期日不晚于当前日的回购按（到期日，被接受序号）序逐一处理。
func (e *Engine) settle() {
	var due []*repoRec
	for _, r := range e.repos {
		if r.status == Outstanding && r.due <= e.day {
			due = append(due, r)
		}
	}
	sort.Slice(due, func(i, j int) bool {
		if due[i].due != due[j].due {
			return due[i].due < due[j].due
		}
		return due[i].seq < due[j].seq
	})
	for _, r := range due {
		e.settleOne(r)
	}
}

func (e *Engine) settleOne(r *repoRec) {
	a, _ := e.lib.Get(r.acct)
	if a.Cash >= r.repay {
		a.Cash -= r.repay
		e.lib.ReleaseUse(r.acct, r.use)
		r.status = Redeemed
		return
	}
	// 违约：不扣现金，按债券代码字节序处置在库券。
	outstanding := r.repay
	for _, bond := range e.lib.PledgedBonds(r.acct) {
		if outstanding == 0 {
			break
		}
		b, _ := e.bonds.Get(bond)
		pledged := a.Pos[bond].Pledged
		sell := min(pledged, ceilDiv(outstanding, b.Price))
		e.lib.RemovePledged(r.acct, bond, sell, b.Rate)
		outstanding -= sell * b.Price
		if outstanding < 0 { // 最后一种多卖出的余款退入账户现金
			a.Cash += -outstanding
			outstanding = 0
		}
	}
	if outstanding > 0 {
		r.badDebt = outstanding
	}
	e.lib.ReleaseUse(r.acct, r.use)
	r.status = Defaulted
}

// AddBond 添加债券：rate 为 0..150，price 为 1..10^6，重复报 ErrDuplicate。
func (e *Engine) AddBond(day int64, bond []byte, rate, price int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !validDay(day) || len(bond) == 0 || rate < 0 || rate > 150 || price < 1 || price > 1_000_000 {
		return ErrParam
	}
	if err := e.advance(day); err != nil {
		return err
	}
	return e.bonds.Add(string(bond), rate, price)
}

// SetRate 调整折算率，只重算在库持有该券的账户。
func (e *Engine) SetRate(day int64, bond []byte, rate int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !validDay(day) || len(bond) == 0 || rate < 0 || rate > 150 {
		return ErrParam
	}
	if err := e.advance(day); err != nil {
		return err
	}
	b, ok := e.bonds.Get(string(bond))
	if !ok {
		return ErrNotExist
	}
	if err := e.bonds.SetRate(string(bond), rate); err != nil {
		return err
	}
	e.lib.Retrate(string(bond), b.Rate, rate)
	return nil
}

// SetPrice 调整净价。
func (e *Engine) SetPrice(day int64, bond []byte, price int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !validDay(day) || len(bond) == 0 || price < 1 || price > 1_000_000 {
		return ErrParam
	}
	if err := e.advance(day); err != nil {
		return err
	}
	return e.bonds.SetPrice(string(bond), price)
}

// Credit 增加可用持仓并建立账户。
func (e *Engine) Credit(day int64, acct, bond []byte, n int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !validDay(day) || len(acct) == 0 || len(bond) == 0 || !validQty(n) {
		return ErrParam
	}
	if err := e.advance(day); err != nil {
		return err
	}
	if !e.bonds.Has(string(bond)) {
		return ErrNotExist
	}
	e.lib.Credit(string(acct), string(bond), n)
	return nil
}

// CreditCash 增加现金并建立账户。
func (e *Engine) CreditCash(day int64, acct []byte, amt int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !validDay(day) || len(acct) == 0 || !validQty(amt) {
		return ErrParam
	}
	if err := e.advance(day); err != nil {
		return err
	}
	e.lib.CreditCash(string(acct), amt)
	return nil
}

// PledgeIn 把 n 张从可用转入质押库；欠库账户不受限制。
func (e *Engine) PledgeIn(day int64, acct, bond []byte, n int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !validDay(day) || len(acct) == 0 || len(bond) == 0 || !validQty(n) {
		return ErrParam
	}
	if err := e.advance(day); err != nil {
		return err
	}
	if _, ok := e.lib.Get(string(acct)); !ok {
		return ErrNotExist
	}
	b, ok := e.bonds.Get(string(bond))
	if !ok {
		return ErrNotExist
	}
	return e.lib.PledgeIn(string(acct), string(bond), n, b.Rate)
}

// Repo 融资回购：要求 Use+ceil(amount/100)<=Cap（取等通过）。
func (e *Engine) Repo(day int64, id, acct []byte, amount, days, r int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !validDay(day) || len(id) == 0 || len(acct) == 0 || !validQty(amount) ||
		days < 1 || days > 365 || r < 0 || r > 10_000 {
		return ErrParam
	}
	if err := e.advance(day); err != nil {
		return err
	}
	a, ok := e.lib.Get(string(acct))
	if !ok {
		return ErrNotExist
	}
	if _, dup := e.repos[string(id)]; dup {
		return ErrDuplicate
	}
	if a.Cap < a.Use {
		return ErrDeficit
	}
	use := ceilDiv(amount, 100)
	if !e.lib.HasCapacity(string(acct), use) {
		return ErrInsufficientCap
	}
	e.seq++
	e.repos[string(id)] = &repoRec{
		acct:   string(acct),
		amount: amount,
		repay:  amount + ceilDiv(amount*r*days, 3_650_000),
		due:    day + days,
		use:    use,
		seq:    e.seq,
	}
	e.lib.AddUse(string(acct), use)
	a.Cash += amount
	return nil
}

// PledgeOut 把 n 张从质押库转回可用；欠库账户一律拒绝。
func (e *Engine) PledgeOut(day int64, acct, bond []byte, n int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !validDay(day) || len(acct) == 0 || len(bond) == 0 || !validQty(n) {
		return ErrParam
	}
	if err := e.advance(day); err != nil {
		return err
	}
	a, ok := e.lib.Get(string(acct))
	if !ok {
		return ErrNotExist
	}
	b, ok := e.bonds.Get(string(bond))
	if !ok {
		return ErrNotExist
	}
	if a.Cap < a.Use {
		return ErrDeficit
	}
	return e.lib.PledgeOut(string(acct), string(bond), n, b.Rate)
}

// Deficits 按账户字节序列出欠库账户与缺口 Use-Cap。
func (e *Engine) Deficits() []Deficit {
	e.mu.Lock()
	defer e.mu.Unlock()
	ds := e.lib.Deficits()
	out := make([]Deficit, len(ds))
	for i, d := range ds {
		out[i] = Deficit{Acct: []byte(d.Acct), Gap: d.Gap}
	}
	return out
}

// RepoInfo 返回回购的可观测信息。
func (e *Engine) RepoInfo(id []byte) (Info, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	r, ok := e.repos[string(id)]
	if !ok {
		return Info{}, false
	}
	return Info{Status: r.status, Amount: r.amount, Repay: r.repay, Due: r.due, BadDebt: r.badDebt}, true
}
