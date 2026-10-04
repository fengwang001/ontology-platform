// Package book 实现开盘集合竞价的委托收集与状态机。
package book

import (
	"errors"
	"sort"
	"sync"

	"ontology/alloc"
	"ontology/auction"
)

// 拒绝原因，彼此可用 errors.Is 区分。
var (
	ErrInvalid   = errors.New("book: invalid argument")
	ErrClockBack = errors.New("book: clock moved backwards")
	ErrPhase     = errors.New("book: operation not allowed in current phase")
	ErrDuplicate = errors.New("book: duplicate order id")
	ErrNotFound  = errors.New("book: order id not found")
	ErrPriceBand = errors.New("book: price outside band")
	ErrAcctLimit = errors.New("book: account order limit reached")
)

// Side 为委托方向。
type Side = auction.Side

const (
	Buy  = auction.Buy
	Sell = auction.Sell
)

// Fill 是一笔成交。
type Fill struct {
	BuyID  []byte
	SellID []byte
	Qty    int64
}

// Rest 是一笔剩余委托。
type Rest struct {
	ID    []byte
	Acct  []byte
	Side  Side
	Price int64
	Qty   int64
}

// Book 是集合竞价委托簿。
type Book struct {
	mu      sync.Mutex
	ref     int64
	tc      int64
	tu      int64
	lo      int64
	hi      int64
	m       int64
	lastNow int64
	opened  bool
	seq     int64
	orders  map[string]*order // id -> 在簿委托
	acctCnt map[string]int64  // 账户 -> 在簿委托数
}

type order struct {
	seq   int64
	id    []byte
	acct  []byte
	side  Side
	price int64
	qty   int64
}

// New 创建委托簿。
func New(ref, tc, tu, lo, hi, m int64) (*Book, error) {
	if ref < 1 || ref > 1e9 || lo < 1 || lo > 1e9 || hi < 1 || hi > 1e9 || lo > ref || ref > hi {
		return nil, ErrInvalid
	}
	if tc < 0 || tc > 1e12 || tu < 0 || tu > 1e12 || tc > tu {
		return nil, ErrInvalid
	}
	if m < 1 || m > 1e5 {
		return nil, ErrInvalid
	}
	return &Book{
		ref:     ref,
		tc:      tc,
		tu:      tu,
		lo:      lo,
		hi:      hi,
		m:       m,
		orders:  map[string]*order{},
		acctCnt: map[string]int64{},
	}, nil
}

// Submit 接受一笔报单。
func (b *Book) Submit(now int64, id, acct []byte, side Side, price, qty int64) (seq int64, err error) {
	if now < 0 || now > 1e12 || len(id) == 0 || len(acct) == 0 ||
		(side != Buy && side != Sell) || qty < 1 || qty > 1e9 {
		return 0, ErrInvalid
	}
	// price 为 1..1e9 属参数非法；价格带校验在编号重复之后。
	if price < 1 || price > 1e9 {
		return 0, ErrInvalid
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	if now < b.lastNow {
		return 0, ErrClockBack
	}
	if b.opened {
		return 0, ErrPhase
	}
	key := string(id)
	if _, ok := b.orders[key]; ok {
		return 0, ErrDuplicate
	}
	if price < b.lo || price > b.hi {
		return 0, ErrPriceBand
	}
	acctKey := string(acct)
	if b.acctCnt[acctKey] >= b.m {
		return 0, ErrAcctLimit
	}

	b.seq++
	b.orders[key] = &order{
		seq:   b.seq,
		id:    append([]byte(nil), id...),
		acct:  append([]byte(nil), acct...),
		side:  side,
		price: price,
		qty:   qty,
	}
	b.acctCnt[acctKey]++
	b.lastNow = now
	return b.seq, nil
}

// Cancel 在可撤期撤销一笔在簿委托。
func (b *Book) Cancel(now int64, id []byte) error {
	if now < 0 || now > 1e12 || len(id) == 0 {
		return ErrInvalid
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	if now < b.lastNow {
		return ErrClockBack
	}
	if b.opened || now >= b.tc {
		return ErrPhase
	}
	o, ok := b.orders[string(id)]
	if !ok {
		return ErrNotFound
	}
	delete(b.orders, string(o.id))
	acctKey := string(o.acct)
	b.acctCnt[acctKey]--
	if b.acctCnt[acctKey] == 0 {
		delete(b.acctCnt, acctKey)
	}
	b.lastNow = now
	return nil
}

// Uncross 开盘并返回开盘价、成交与剩余。
func (b *Book) Uncross(now int64) (price int64, fills []Fill, rests []Rest, err error) {
	if now < 0 || now > 1e12 {
		return 0, nil, nil, ErrInvalid
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	if now < b.lastNow {
		return 0, nil, nil, ErrClockBack
	}
	if b.opened || now < b.tu {
		return 0, nil, nil, ErrPhase
	}

	ords := make([]auction.Order, 0, len(b.orders))
	for _, o := range b.orders {
		ords = append(ords, auction.Order{
			Seq:   o.seq,
			ID:    o.id,
			Acct:  o.acct,
			Side:  o.side,
			Price: o.price,
			Qty:   o.qty,
		})
	}
	sort.Slice(ords, func(i, j int) bool { return ords[i].Seq < ords[j].Seq })

	res := auction.Price(ords, b.ref)
	af, ar := alloc.Match(ords, res.Price)

	fills = make([]Fill, 0, len(af))
	for _, f := range af {
		fills = append(fills, Fill{
			BuyID:  append([]byte(nil), f.BuyID...),
			SellID: append([]byte(nil), f.SellID...),
			Qty:    f.Qty,
		})
	}
	rests = make([]Rest, 0, len(ar))
	for _, r := range ar {
		rests = append(rests, Rest{
			ID:    append([]byte(nil), r.ID...),
			Acct:  append([]byte(nil), r.Acct...),
			Side:  r.Side,
			Price: r.Price,
			Qty:   r.Qty,
		})
	}
	b.opened = true
	b.lastNow = now
	return res.Price, fills, rests, nil
}
