// Package instr 是券款对付交收的指令簿：账户持券/现金、现价、指令与应收应付账。
package instr

import (
	"errors"
	"sort"
	"sync"
)

// 哨兵错误：按拒绝次序排列，errors.Is 可区分。
var (
	ErrInvalid   = errors.New("instr: invalid argument")
	ErrRollback  = errors.New("instr: day rollback")
	ErrDuplicate = errors.New("instr: duplicate instruction id")
	ErrNotFound  = errors.New("instr: instruction id not found")
	ErrNoPrice   = errors.New("instr: symbol has no price")
	ErrState     = errors.New("instr: illegal instruction state")
)

// Status 是指令状态。
type Status int

const (
	Open Status = iota
	Settled
	Cancelled
	BoughtIn
)

// String 返回状态名，便于日志与对账。
func (s Status) String() string {
	switch s {
	case Settled:
		return "SETTLED"
	case Cancelled:
		return "CANCELLED"
	case BoughtIn:
		return "BOUGHT_IN"
	default:
		return "OPEN"
	}
}

// Instruction 是一条交收指令的对外只读视图。
type Instruction struct {
	Seq             int
	ID              string
	Seller, Buyer   string
	Sym             string
	Qty, Amount     int64
	SD              int
	Delivered, Paid int64
	Status          Status
	SellerFine      int64 // 卖方累计罚金（应付）
	BuyerFine       int64 // 买方累计罚金（应付）
	SellerComp      int64 // 卖方买入赔付（应付）
	BuyerComp       int64 // 买方买入赔付（应收）
}

// order 是指令的内部可变表示。
type order struct {
	seq                          int
	id, seller, buyer, sym       string
	qty, amount, delivered, paid int64
	sd                           int
	status                       Status
	sellerFine, buyerFine        int64
	sellerComp, buyerComp        int64
}

func (o *order) snapshot() Instruction {
	return Instruction{
		Seq: o.seq, ID: o.id, Seller: o.seller, Buyer: o.buyer, Sym: o.sym,
		Qty: o.qty, Amount: o.amount, SD: o.sd,
		Delivered: o.delivered, Paid: o.paid, Status: o.status,
		SellerFine: o.sellerFine, BuyerFine: o.buyerFine,
		SellerComp: o.sellerComp, BuyerComp: o.buyerComp,
	}
}

// Book 是并发安全的指令簿。
type Book struct {
	mu sync.Mutex

	u, rs, rb int64
	a         int

	maxDay     int64
	lastSettle int64 // 最近一次成功 RunSettle 的 day；-1 表示从未
	touched    int   // 最近一次 RunSettle 触碰的指令数
	nextSeq    int
	orders     map[string]*order
	holdings   map[string]map[string]int64 // acct -> sym -> qty
	cash       map[string]int64
	prices     map[string]int64
	payable    map[string]int64 // 应付账（罚金+买入赔付）
	receivable map[string]int64 // 应收账（买入赔付）
}

// New 创建指令簿。参数非法时 panic（构造期错误，不属于操作拒绝次序）。
func New(u, rs, rb int64, a int) *Book {
	if u < 1 || u > 1_000_000 || rs < 0 || rs > 10_000 || rb < 0 || rb > 10_000 ||
		a < 1 || a > 100 {
		panic("instr: New: invalid parameter")
	}
	return &Book{
		u: u, rs: rs, rb: rb, a: a,
		lastSettle: -1,
		orders:     map[string]*order{},
		holdings:   map[string]map[string]int64{},
		cash:       map[string]int64{},
		prices:     map[string]int64{},
		payable:    map[string]int64{},
		receivable: map[string]int64{},
	}
}

// ---- 参数与日期校验（调用方持锁）----

func validDay(day int) bool { return day >= 0 && day <= 1_000_000 }

func validBytes(s string) bool { return len(s) > 0 }

// checkDay 按拒绝次序处理 day：先参数非法，再日期回退。
func (b *Book) checkDay(day int) error {
	if !validDay(day) {
		return ErrInvalid
	}
	if int64(day) < b.maxDay {
		return ErrRollback
	}
	return nil
}

func (b *Book) commitDay(day int) {
	if int64(day) > b.maxDay {
		b.maxDay = int64(day)
	}
}

// Credit 增加某账户某标的持券。
func (b *Book) Credit(day int, acct, sym string, qty int64) error {
	if !validDay(day) || !validBytes(acct) || !validBytes(sym) ||
		qty < 1 || qty > 1_000_000_000_000 {
		return ErrInvalid
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.checkDay(day); err != nil {
		return err
	}
	if b.holdings[acct] == nil {
		b.holdings[acct] = map[string]int64{}
	}
	b.holdings[acct][sym] += qty
	b.commitDay(day)
	return nil
}

// CreditCash 增加某账户现金。
func (b *Book) CreditCash(day int, acct string, amt int64) error {
	if !validDay(day) || !validBytes(acct) || amt < 1 || amt > 1_000_000_000_000 {
		return ErrInvalid
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.checkDay(day); err != nil {
		return err
	}
	b.cash[acct] += amt
	b.commitDay(day)
	return nil
}

// SetPrice 设定某标的当日现价。
func (b *Book) SetPrice(day int, sym string, price int64) error {
	if !validDay(day) || !validBytes(sym) || price < 1 || price > 1_000_000 {
		return ErrInvalid
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.checkDay(day); err != nil {
		return err
	}
	b.prices[sym] = price
	b.commitDay(day)
	return nil
}

// Instruct 接受一条交收指令。
func (b *Book) Instruct(day int, id, seller, buyer, sym string, qty, amount int64, sd int) error {
	// 1) 参数非法（无需持锁的字面值校验）
	if !validDay(day) || !validBytes(id) || !validBytes(seller) || !validBytes(buyer) ||
		!validBytes(sym) || seller == buyer ||
		qty <= 0 || qty > 1_000_000_000 || qty%b.u != 0 ||
		amount < 1 || amount > 1_000_000_000_000_000 ||
		sd <= day || !validDay(sd) {
		return ErrInvalid
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	// 2) 日期回退
	if err := b.checkDay(day); err != nil {
		return err
	}
	// 3) 编号重复 / 标的无现价
	if _, ok := b.orders[id]; ok {
		return ErrDuplicate
	}
	if _, ok := b.prices[sym]; !ok {
		return ErrNoPrice
	}
	b.nextSeq++
	b.orders[id] = &order{
		seq: b.nextSeq, id: id, seller: seller, buyer: buyer, sym: sym,
		qty: qty, amount: amount, sd: sd, status: Open,
	}
	b.commitDay(day)
	return nil
}

// Cancel 撤销未了结且未到期的指令。
func (b *Book) Cancel(day int, id string) error {
	if !validDay(day) || !validBytes(id) {
		return ErrInvalid
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.checkDay(day); err != nil {
		return err
	}
	o, ok := b.orders[id]
	if !ok {
		return ErrNotFound
	}
	// 仅 day<sd 且未了结可撤；到期（day>=sd）或已了结均状态不符。
	if o.status != Open || day >= o.sd {
		return ErrState
	}
	o.status = Cancelled
	b.commitDay(day)
	return nil
}

// ---- 只读查询（加锁，返回值拷贝）----

// Get 返回指令视图与是否存在。
func (b *Book) Get(id string) (Instruction, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.GetLocked(id)
}

// GetLocked 是 Get 的持锁版本，供 settle 在批处理锁内使用。
func (b *Book) GetLocked(id string) (Instruction, bool) {
	o, ok := b.orders[id]
	if !ok {
		return Instruction{}, false
	}
	return o.snapshot(), true
}

// Holdings 返回某账户某标的持券。
func (b *Book) Holdings(acct, sym string) int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.holdings[acct][sym]
}

// Cash 返回某账户现金。
func (b *Book) Cash(acct string) int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.cash[acct]
}

// Price 返回某标的现价与是否存在。
func (b *Book) Price(sym string) (int64, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	p, ok := b.prices[sym]
	return p, ok
}

// Payable 返回账户应付账（罚金+赔付）。
func (b *Book) Payable(acct string) int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.payable[acct]
}

// Receivable 返回账户应收账（赔付）。
func (b *Book) Receivable(acct string) int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.receivable[acct]
}

// Params 返回构造参数 U、卖方罚金率、买方罚金率、买入日龄。
func (b *Book) Params() (u, rs, rb int64, age int) {
	return b.u, b.rs, b.rb, b.a
}

// Touched 返回最近一次 RunSettle 触碰的指令数。
func (b *Book) Touched() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.touched
}

// MaxDay 返回已接受操作的最大 day。
func (b *Book) MaxDay() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return int(b.maxDay)
}

// AllForTest 返回全部指令视图（按序号升序），仅供测试对账。
func (b *Book) AllForTest() []Instruction {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]Instruction, 0, len(b.orders))
	for _, o := range b.orders {
		out = append(out, o.snapshot())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Seq < out[j].Seq })
	return out
}

// ---- 供 settle 包在同一把锁内使用的原语（不加锁，调用方必须持锁）----

// Lock/Unlock 暴露内部锁给 settle，使整批处理原子、可线性化。
func (b *Book) Lock()   { b.mu.Lock() }
func (b *Book) Unlock() { b.mu.Unlock() }

// BeginRun 校验批处理日期并登记：参数非法 > 日期回退 > 重复执行（状态不符）。
// 通过后推进 lastSettle/maxDay；被拒绝时不改任何状态。
func (b *Book) BeginRun(day int) error {
	if !validDay(day) {
		return ErrInvalid
	}
	if int64(day) < b.maxDay {
		return ErrRollback
	}
	if int64(day) == b.lastSettle {
		return ErrState
	}
	b.lastSettle = int64(day)
	b.commitDay(day)
	return nil
}

// OpenDue 返回 sd<=day 的未了结指令，按 (sd, seq) 升序（快照）。
func (b *Book) OpenDue(day int) []Instruction {
	out := make([]Instruction, 0)
	for _, o := range b.orders {
		if o.status == Open && o.sd <= day {
			out = append(out, o.snapshot())
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].SD != out[j].SD {
			return out[i].SD < out[j].SD
		}
		return out[i].Seq < out[j].Seq
	})
	return out
}

// SetTouched 由 settle 记录本批触碰的指令数。
func (b *Book) SetTouched(n int) { b.touched = n }

// HoldingsLocked / CashLocked / PriceLocked 为持锁读取。
func (b *Book) HoldingsLocked(acct, sym string) int64 { return b.holdings[acct][sym] }
func (b *Book) CashLocked(acct string) int64          { return b.cash[acct] }
func (b *Book) PriceLocked(sym string) int64          { return b.prices[sym] }

// AddPayable / AddReceivable 记应付/应收账（罚金与赔付，不动券款）。
func (b *Book) AddPayable(acct string, m int64)    { b.payable[acct] += m }
func (b *Book) AddReceivable(acct string, m int64) { b.receivable[acct] += m }

// ApplyDelivery 记录指令一次交付：累加 delivered/paid，券款即时过户；
// 若交完则置已交收。返回更新后的指令视图。
func (b *Book) ApplyDelivery(id string, k, pay int64) Instruction {
	o := b.orders[id]
	o.delivered += k
	o.paid += pay
	if b.holdings[o.seller] == nil {
		b.holdings[o.seller] = map[string]int64{}
	}
	b.holdings[o.seller][o.sym] -= k
	if b.holdings[o.buyer] == nil {
		b.holdings[o.buyer] = map[string]int64{}
	}
	b.holdings[o.buyer][o.sym] += k
	b.cash[o.buyer] -= pay
	b.cash[o.seller] += pay
	if o.delivered == o.qty {
		o.status = Settled
	}
	return o.snapshot()
}

// AddFines 给指令记卖方/买方当日罚金，并同步进各自应付账。
func (b *Book) AddFines(id string, sellerFine, buyerFine int64) {
	o := b.orders[id]
	o.sellerFine += sellerFine
	o.buyerFine += buyerFine
	b.payable[o.seller] += sellerFine
	b.payable[o.buyer] += buyerFine
}

// BuyIn 将指令置为已买入：卖方赔付 comp（应付），买方同额应收。
func (b *Book) BuyIn(id string, comp int64) {
	o := b.orders[id]
	o.status = BoughtIn
	o.sellerComp += comp
	o.buyerComp += comp
	b.payable[o.seller] += comp
	b.receivable[o.buyer] += comp
}

// CancelOpen 将仍未了结的指令置为已取消（逾期仅买方有责）。
func (b *Book) CancelOpen(id string) {
	if o := b.orders[id]; o.status == Open {
		o.status = Cancelled
	}
}
