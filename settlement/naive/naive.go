// Package naive 是按需求文字逐行重写的独立参考模型，
// 与 settlement 引擎不共享任何数据结构，用于随机差分测试。
package naive

import (
	"errors"
	"sort"
)

var (
	ErrInvalidParam   = errors.New("invalid parameter")
	ErrNonBusinessDay = errors.New("not a business day")
	ErrOrdering       = errors.New("batch ordering error")
	ErrDuplicateID    = errors.New("duplicate order id")
	ErrAccountMissing = errors.New("account does not exist")
	ErrDatePassed     = errors.New("settlement date already passed")
)

// Account 为朴素模型账户。
type Account struct {
	Name     string
	Sec      map[int64]int64
	Cash     int64
	FeePay   int64
	FeeRecv  int64
	CompPay  int64
	CompRecv int64
}

// Order 为朴素模型指令。
type Order struct {
	ID, Security, Qty, Price, SettleDay int64
	Buyer, Seller                       string
	AllowPartial                        bool
	Delivered                           int64
	FailDays                            int
	// Status: 0 pending, 1 partial, 2 complete, 3 force_closed
	Status int
}

// Model 为朴素模型。
type Model struct {
	Days    []int64
	B       int
	BPS     int64
	Accts   map[string]*Account
	Orders  map[int64]*Order
	LastDay int64
}

// New 构造朴素模型。
func New(days []int64, maxFail int, bps int64) (*Model, error) {
	if len(days) == 0 || maxFail <= 0 || bps < 0 {
		return nil, ErrInvalidParam
	}
	d := append([]int64(nil), days...)
	sort.Slice(d, func(i, j int) bool { return d[i] < d[j] })
	for i := 1; i < len(d); i++ {
		if d[i] == d[i-1] {
			return nil, ErrInvalidParam
		}
	}
	return &Model{
		Days:   d,
		B:      maxFail,
		BPS:    bps,
		Accts:  map[string]*Account{},
		Orders: map[int64]*Order{},
	}, nil
}

// AddAccount 登记账户。
func (m *Model) AddAccount(name string, sec map[int64]int64, cash int64) error {
	if name == "" || cash < 0 {
		return ErrInvalidParam
	}
	cp := map[int64]int64{}
	for k, v := range sec {
		if v < 0 {
			return ErrInvalidParam
		}
		cp[k] = v
	}
	if _, ok := m.Accts[name]; ok {
		return ErrInvalidParam
	}
	m.Accts[name] = &Account{Name: name, Sec: cp, Cash: cash}
	return nil
}

func indexOf(days []int64, v int64) int {
	i := sort.Search(len(days), func(i int) bool { return days[i] >= v })
	if i < len(days) && days[i] == v {
		return i
	}
	return -1
}

// Register 登记指令（错误优先级与引擎一致）。
func (m *Model) Register(o Order) error {
	if o.ID <= 0 || o.Security <= 0 || o.Buyer == "" || o.Seller == "" ||
		o.Buyer == o.Seller || o.Qty <= 0 || o.Price <= 0 || o.SettleDay <= 0 {
		return ErrInvalidParam
	}
	if _, dup := m.Orders[o.ID]; dup {
		return ErrDuplicateID
	}
	if _, bok := m.Accts[o.Buyer]; !bok {
		return ErrAccountMissing
	}
	if _, sok := m.Accts[o.Seller]; !sok {
		return ErrAccountMissing
	}
	if indexOf(m.Days, o.SettleDay) < 0 {
		return ErrInvalidParam
	}
	if m.LastDay != 0 && o.SettleDay < m.LastDay {
		return ErrDatePassed
	}
	cp := o
	m.Orders[o.ID] = &cp
	return nil
}

// Batch 朴素重放某营业日批处理（每次从头扫描全部未了结指令）。
func (m *Model) Batch(day int64, ref map[int64]int64) error {
	if day <= 0 || ref == nil {
		return ErrInvalidParam
	}
	for sec, p := range ref {
		if sec <= 0 || p <= 0 {
			return ErrInvalidParam
		}
	}
	if indexOf(m.Days, day) < 0 {
		return ErrNonBusinessDay
	}
	var expect int64
	if m.LastDay == 0 {
		expect = m.Days[0]
	} else {
		pi := indexOf(m.Days, m.LastDay)
		if pi+1 >= len(m.Days) {
			return ErrOrdering
		}
		expect = m.Days[pi+1]
	}
	if day != expect {
		return ErrOrdering
	}

	// 候选：应交割日 <= 当日且未了结，按编号升序。
	var cand []*Order
	for _, o := range m.Orders {
		if o.SettleDay <= day && o.Status != 2 && o.Status != 3 {
			cand = append(cand, o)
		}
	}
	sort.Slice(cand, func(i, j int) bool { return cand[i].ID < cand[j].ID })
	for _, o := range cand {
		if _, ok := ref[o.Security]; !ok {
			return ErrInvalidParam
		}
	}

	// 当日开始头寸快照 + 批内累计占用。
	startSec := map[string]map[int64]int64{}
	startCash := map[string]int64{}
	for n, a := range m.Accts {
		startCash[n] = a.Cash
		s := map[int64]int64{}
		for k, v := range a.Sec {
			s[k] = v
		}
		startSec[n] = s
	}
	usedSec := map[string]map[int64]int64{}
	usedCash := map[string]int64{}

	type move struct {
		buyer, seller  string
		sec, qty, cash int64
	}
	var moves []move
	type failRec struct {
		o          *Order
		rem        int64
		sellerResp bool
	}
	var fails []failRec

	for _, o := range cand {
		rem := o.Qty - o.Delivered
		if _, ok := usedSec[o.Seller]; !ok {
			usedSec[o.Seller] = map[int64]int64{}
		}
		sAvail := startSec[o.Seller][o.Security] - usedSec[o.Seller][o.Security]
		bCash := startCash[o.Buyer] - usedCash[o.Buyer]
		bMax := bCash / o.Price
		can := rem
		if sAvail < can {
			can = sAvail
		}
		if bMax < can {
			can = bMax
		}
		if !o.AllowPartial && can < rem {
			can = 0
		}
		if can > 0 {
			moves = append(moves, move{o.Buyer, o.Seller, o.Security, can, can * o.Price})
			usedSec[o.Seller][o.Security] += can
			usedCash[o.Buyer] += can * o.Price
			o.Delivered += can
		}
		rem -= can
		if rem == 0 {
			o.Status = 2
			continue
		}
		sellerResp := sAvail < rem
		if o.Delivered > 0 {
			o.Status = 1
		}
		fails = append(fails, failRec{o, rem, sellerResp})
	}

	// 统一提交交割（券款对付）。
	for _, mv := range moves {
		m.Accts[mv.seller].Sec[mv.sec] -= mv.qty
		m.Accts[mv.buyer].Sec[mv.sec] += mv.qty
		m.Accts[mv.seller].Cash += mv.cash
		m.Accts[mv.buyer].Cash -= mv.cash
	}

	// 罚金与强制了结。
	for _, f := range fails {
		o := f.o
		o.FailDays++
		cashAmt := f.rem * o.Price
		pen := cashAmt * m.BPS / 10000
		if cashAmt*m.BPS%10000 != 0 {
			pen++
		}
		resp, other := o.Seller, o.Buyer
		if !f.sellerResp {
			resp, other = o.Buyer, o.Seller
		}
		m.Accts[resp].FeePay += pen
		m.Accts[other].FeeRecv += pen
		if o.FailDays >= m.B {
			o.Status = 3
			if f.sellerResp {
				comp := ref[o.Security]*f.rem - cashAmt
				if comp < 0 {
					comp = 0
				}
				m.Accts[o.Seller].CompPay += comp
				m.Accts[o.Buyer].CompRecv += comp
			}
		}
	}

	m.LastDay = day
	return nil
}
