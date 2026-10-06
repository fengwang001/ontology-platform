// Package instr 实现券款对付交收的指令簿：账户（持券/现金/应付/应收）、
// 现价、交收指令的登记与注销，以及供 settle/fail 引擎使用的持锁访问接口。
package instr

import (
	"errors"
	"sort"
	"sync"
)

// 各类拒绝原因，可用 errors.Is 区分。
var (
	ErrParam     = errors.New("instr: invalid parameter")
	ErrDate      = errors.New("instr: day rollback")
	ErrDuplicate = errors.New("instr: duplicate instruction id")
	ErrNotFound  = errors.New("instr: unknown instruction id")
	ErrNoPrice   = errors.New("instr: no price for symbol")
	ErrState     = errors.New("instr: state mismatch")
)

const (
	maxDay    = 1_000_000
	maxUnit   = 1_000_000
	maxRate   = 10_000
	maxAge    = 100
	maxQty    = 1_000_000_000
	maxAmount = 1_000_000_000_000_000
	maxCredit = 1_000_000_000_000
	maxPrice  = 1_000_000
	rateBase  = 10_000
)

// Status 为指令的存续状态。
type Status int

const (
	Open      Status = iota // 未了结
	Settled                 // 已交收
	BoughtIn                // 已买入（逾期强制买入）
	Cancelled               // 已取消
)

func (s Status) String() string {
	switch s {
	case Open:
		return "open"
	case Settled:
		return "settled"
	case BoughtIn:
		return "bought-in"
	case Cancelled:
		return "cancelled"
	}
	return "unknown"
}

// Instruction 为一条交收指令。SellerFault/BuyerFault 是当日归责快照，
// 仅在最近一次 RunSettle 当日有意义。
type Instruction struct {
	Seq         int
	ID          string
	Seller      string
	Buyer       string
	Sym         string
	Qty         int64
	Amount      int64
	Sd          int
	Delivered   int64
	Paid        int64
	Status      Status
	SellerFault bool
	BuyerFault  bool
}

// Account 为一个账户的台账。罚金与赔付只记入 Payable/Receivable，
// 不动用 Cash 与 Holdings。
type Account struct {
	Holdings   map[string]int64
	Cash       int64
	Payable    int64
	Receivable int64
}

// Book 为指令簿。所有公开操作可并发调用，结果等价于某个串行顺序。
type Book struct {
	mu      sync.Mutex
	unit    int64
	rs      int64
	rb      int64
	age     int
	maxDay  int
	accts   map[string]*Account
	prices  map[string]int64
	byID    map[string]*Instruction
	order   []*Instruction
	settled map[int]bool
	touched int
}

// New 创建指令簿：U 为最小交付单位，rs/rb 为卖方/买方罚金率（万分比），
// A 为买入日龄。
func New(U, rs, rb int64, A int) (*Book, error) {
	if U < 1 || U > maxUnit || rs < 0 || rs > maxRate ||
		rb < 0 || rb > maxRate || A < 1 || A > maxAge {
		return nil, ErrParam
	}
	return &Book{
		unit:    U,
		rs:      rs,
		rb:      rb,
		age:     A,
		maxDay:  -1,
		accts:   map[string]*Account{},
		prices:  map[string]int64{},
		byID:    map[string]*Instruction{},
		settled: map[int]bool{},
	}, nil
}

// checkDay 校验日期：越界为参数非法，回退为日期错误。调用方须持锁。
func (b *Book) checkDay(day int) error {
	if day < 0 || day > maxDay {
		return ErrParam
	}
	if day < b.maxDay {
		return ErrDate
	}
	return nil
}

// advance 推进已接受的最大日期。调用方须持锁且已完成全部校验。
func (b *Book) advance(day int) {
	if day > b.maxDay {
		b.maxDay = day
	}
}

// acct 返回账户，不存在则创建。调用方须持锁。
func (b *Book) acct(name string) *Account {
	a, ok := b.accts[name]
	if !ok {
		a = &Account{Holdings: map[string]int64{}}
		b.accts[name] = a
	}
	return a
}

// Credit 增加账户持券。
func (b *Book) Credit(day int, acct, sym string, qty int64) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if acct == "" || sym == "" || qty < 1 || qty > maxCredit {
		return ErrParam
	}
	if err := b.checkDay(day); err != nil {
		return err
	}
	b.advance(day)
	a := b.acct(acct)
	a.Holdings[sym] += qty
	return nil
}

// CreditCash 增加账户现金。
func (b *Book) CreditCash(day int, acct string, amt int64) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if acct == "" || amt < 1 || amt > maxCredit {
		return ErrParam
	}
	if err := b.checkDay(day); err != nil {
		return err
	}
	b.advance(day)
	b.acct(acct).Cash += amt
	return nil
}

// SetPrice 设定标的现价。
func (b *Book) SetPrice(day int, sym string, price int64) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if sym == "" || price < 1 || price > maxPrice {
		return ErrParam
	}
	if err := b.checkDay(day); err != nil {
		return err
	}
	b.advance(day)
	b.prices[sym] = price
	return nil
}

// Instruct 登记一条交收指令，按被接受次序编号。
func (b *Book) Instruct(day int, id, seller, buyer, sym string, qty, amount int64, sd int) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if id == "" || seller == "" || buyer == "" || sym == "" ||
		qty < 1 || qty > maxQty || qty%b.unit != 0 ||
		amount < 1 || amount > maxAmount ||
		sd <= day || seller == buyer {
		return ErrParam
	}
	if err := b.checkDay(day); err != nil {
		return err
	}
	if _, ok := b.byID[id]; ok {
		return ErrDuplicate
	}
	if _, ok := b.prices[sym]; !ok {
		return ErrNoPrice
	}
	b.advance(day)
	ins := &Instruction{
		Seq:    len(b.order),
		ID:     id,
		Seller: seller,
		Buyer:  buyer,
		Sym:    sym,
		Qty:    qty,
		Amount: amount,
		Sd:     sd,
		Status: Open,
	}
	b.byID[id] = ins
	b.order = append(b.order, ins)
	return nil
}

// Cancel 注销指令：仅在 day<sd 且指令未了结时允许。
func (b *Book) Cancel(day int, id string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if id == "" {
		return ErrParam
	}
	if err := b.checkDay(day); err != nil {
		return err
	}
	ins, ok := b.byID[id]
	if !ok {
		return ErrNotFound
	}
	if ins.Status != Open || day >= ins.Sd {
		return ErrState
	}
	b.advance(day)
	ins.Status = Cancelled
	return nil
}

// --- 引擎接口：供 settle/fail 包使用，调用方必须先调用 Lock 持锁。 ---

// Lock 锁住指令簿，供结算引擎整批处理期间独占。
func (b *Book) Lock() { b.mu.Lock() }

// Unlock 解锁指令簿。
func (b *Book) Unlock() { b.mu.Unlock() }

// BeginSettle 校验并登记一个交收日：每个 day 至多成功一次。
func (b *Book) BeginSettle(day int) error {
	if err := b.checkDay(day); err != nil {
		return err
	}
	if b.settled[day] {
		return ErrState
	}
	b.advance(day)
	b.settled[day] = true
	return nil
}

// Due 返回 sd<=day 的未了结指令，按 (sd, 序号) 升序。
func (b *Book) Due(day int) []*Instruction {
	var out []*Instruction
	for _, ins := range b.order {
		if ins.Status == Open && ins.Sd <= day {
			out = append(out, ins)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Sd != out[j].Sd {
			return out[i].Sd < out[j].Sd
		}
		return out[i].Seq < out[j].Seq
	})
	return out
}

// NoteTouched 将本日触碰的指令计数加一。
func (b *Book) NoteTouched() { b.touched++ }

// Unit 返回最小交付单位。
func (b *Book) Unit() int64 { return b.unit }

// Rates 返回卖方/买方罚金率（万分比）。
func (b *Book) Rates() (rs, rb int64) { return b.rs, b.rb }

// BuyInAge 返回买入日龄 A。
func (b *Book) BuyInAge() int { return b.age }

// Price 返回标的现价；无现价时返回 0。
func (b *Book) Price(sym string) int64 { return b.prices[sym] }

// Holding 返回账户对某标的的持券。
func (b *Book) Holding(acct, sym string) int64 {
	if a, ok := b.accts[acct]; ok {
		return a.Holdings[sym]
	}
	return 0
}

// Cash 返回账户现金。
func (b *Book) Cash(acct string) int64 {
	if a, ok := b.accts[acct]; ok {
		return a.Cash
	}
	return 0
}

// AddHolding 调整账户持券（可为负增量）。
func (b *Book) AddHolding(acct, sym string, delta int64) {
	a := b.acct(acct)
	a.Holdings[sym] += delta
}

// AddCash 调整账户现金（可为负增量）。
func (b *Book) AddCash(acct string, delta int64) {
	b.acct(acct).Cash += delta
}

// AddPayable 增加账户应付账。
func (b *Book) AddPayable(acct string, amt int64) {
	b.acct(acct).Payable += amt
}

// AddReceivable 增加账户应收账。
func (b *Book) AddReceivable(acct string, amt int64) {
	b.acct(acct).Receivable += amt
}

// Touched 返回历次 RunSettle 触碰的指令总数。
func (b *Book) Touched() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.touched
}

// AccountView 为账户台账的只读快照。
type AccountView struct {
	Holdings   map[string]int64
	Cash       int64
	Payable    int64
	Receivable int64
}

// State 为指令簿整体状态的只读快照。
type State struct {
	Accounts     map[string]AccountView
	Instructions []Instruction
}

// Snapshot 返回指令簿的深拷贝快照，供查询与测试。
func (b *Book) Snapshot() State {
	b.mu.Lock()
	defer b.mu.Unlock()
	st := State{Accounts: map[string]AccountView{}}
	for name, a := range b.accts {
		h := make(map[string]int64, len(a.Holdings))
		for sym, q := range a.Holdings {
			h[sym] = q
		}
		st.Accounts[name] = AccountView{
			Holdings:   h,
			Cash:       a.Cash,
			Payable:    a.Payable,
			Receivable: a.Receivable,
		}
	}
	for _, ins := range b.order {
		st.Instructions = append(st.Instructions, *ins)
	}
	return st
}
