// Package lot 维护按（账户，合约，方向）划分的期货持仓批次。
package lot

import "errors"

var (
	ErrYesterdayShort = errors.New("lot: 昨仓不足")
	ErrTodayShort     = errors.New("lot: 今仓不足")
	ErrPositionShort  = errors.New("lot: 持仓不足")
)

// Direction 为持仓方向，多头或空头，两个方向互不抵消。
type Direction int

const (
	Long Direction = iota
	Short
)

// Valid 报告方向是否合法。
func (d Direction) Valid() bool { return d == Long || d == Short }

// Sign 对多头返回 +1，空头返回 -1。
func (d Direction) Sign() int64 {
	if d == Long {
		return 1
	}
	return -1
}

// Batch 为一次开仓追加的今仓批次。
type Batch struct {
	Price int64
	Qty   int64
}

// Position 为（账户，合约，方向）下的一份持仓。
type Position struct {
	Qy      int64   // 昨仓手数，以 Sp0 计价
	Sp0     int64   // 上一结算价
	Today   []Batch // 今仓批次 FIFO 队列
	touched int     // 一次平仓触碰的今仓批次数
}

// TodayQty 返回今仓总手数。
func (p *Position) TodayQty() int64 {
	var q int64
	for _, b := range p.Today {
		q += b.Qty
	}
	return q
}

// TotalQty 返回昨仓与今仓总手数。
func (p *Position) TotalQty() int64 { return p.Qy + p.TodayQty() }

// Open 追加一个今仓批次。
func (p *Position) Open(price, qty int64) {
	p.Today = append(p.Today, Batch{Price: price, Qty: qty})
}

// CloseYesterday 只平昨仓，不足报 ErrYesterdayShort，全有或全无。
func (p *Position) CloseYesterday(qty int64) error {
	if p.Qy < qty {
		return ErrYesterdayShort
	}
	p.Qy -= qty
	return nil
}

// CloseToday 只平今仓，先开先平，不足报 ErrTodayShort，全有或全无。
// 返回被平的批次（队首部分平仓时已切分）。
func (p *Position) CloseToday(qty int64) ([]Batch, error) {
	if p.TodayQty() < qty {
		return nil, ErrTodayShort
	}
	return p.consumeToday(qty), nil
}

// CloseAuto 先平昨仓，再按先开先平平今仓，总量不足报 ErrPositionShort。
func (p *Position) CloseAuto(qty int64) (yQty int64, today []Batch, err error) {
	if p.TotalQty() < qty {
		return 0, nil, ErrPositionShort
	}
	yQty = qty
	if yQty > p.Qy {
		yQty = p.Qy
	}
	p.Qy -= yQty
	if rest := qty - yQty; rest > 0 {
		today = p.consumeToday(rest)
	}
	return yQty, today, nil
}

// Absorb 结算后把今仓全部并入昨仓，并把上一结算价置为 sp。
func (p *Position) Absorb(sp int64) {
	p.Qy += p.TodayQty()
	p.Today = nil
	p.Sp0 = sp
}

// consumeToday 按先开先平消费今仓 qty 手，调用前须保证手数充足。
// touched 统计触碰的批次数：完全平掉的批次数加至多一个部分批次。
func (p *Position) consumeToday(qty int64) []Batch {
	p.touched = 0
	var out []Batch
	for qty > 0 {
		head := p.Today[0]
		p.touched++
		if head.Qty <= qty {
			out = append(out, head)
			qty -= head.Qty
			p.Today = p.Today[1:]
			continue
		}
		out = append(out, Batch{Price: head.Price, Qty: qty})
		p.Today[0].Qty -= qty
		qty = 0
	}
	return out
}

// Key 为持仓划分键（账户，合约，方向）。
type Key struct {
	Acct string
	Sym  string
	Dir  Direction
}

// Book 为全部持仓的集合。
type Book struct {
	positions map[Key]*Position
}

// NewBook 创建空持仓簿。
func NewBook() *Book { return &Book{positions: make(map[Key]*Position)} }

// Get 返回键对应的持仓，不存在时返回 nil。
func (b *Book) Get(k Key) *Position { return b.positions[k] }

// Ensure 返回键对应的持仓，不存在时创建。
func (b *Book) Ensure(k Key) *Position {
	p, ok := b.positions[k]
	if !ok {
		p = &Position{}
		b.positions[k] = p
	}
	return p
}

// Each 遍历全部持仓。
func (b *Book) Each(fn func(Key, *Position)) {
	for k, p := range b.positions {
		fn(k, p)
	}
}
