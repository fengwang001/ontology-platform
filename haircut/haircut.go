// Package haircut 维护债券的折算率与净价参数，提供标准券额度的逐券取整规则，
// 并定义全系统共用的错误哨兵。
package haircut

import "errors"

var (
	ErrParam             = errors.New("参数非法")
	ErrDayRollback       = errors.New("日期回退")
	ErrNotExist          = errors.New("不存在")
	ErrDuplicate         = errors.New("重复")
	ErrDeficit           = errors.New("欠库")
	ErrInsufficientAvail = errors.New("可用不足")
	ErrInsufficientStock = errors.New("库存不足")
	ErrInsufficientCap   = errors.New("标准券不足")
)

// Bond 为债券参数：Rate 为折算率（0..150，每张折合标准券的百分比），
// Price 为净价（1..10^6，每张）。
type Bond struct {
	Rate  int64
	Price int64
}

// Table 为债券参数表，键为债券代码字节串。
type Table struct {
	bonds map[string]Bond
}

func NewTable() *Table {
	return &Table{bonds: make(map[string]Bond)}
}

func (t *Table) Add(bond string, rate, price int64) error {
	if _, ok := t.bonds[bond]; ok {
		return ErrDuplicate
	}
	t.bonds[bond] = Bond{Rate: rate, Price: price}
	return nil
}

func (t *Table) SetRate(bond string, rate int64) error {
	b, ok := t.bonds[bond]
	if !ok {
		return ErrNotExist
	}
	b.Rate = rate
	t.bonds[bond] = b
	return nil
}

func (t *Table) SetPrice(bond string, price int64) error {
	b, ok := t.bonds[bond]
	if !ok {
		return ErrNotExist
	}
	b.Price = price
	t.bonds[bond] = b
	return nil
}

func (t *Table) Get(bond string) (Bond, bool) {
	b, ok := t.bonds[bond]
	return b, ok
}

func (t *Table) Has(bond string) bool {
	_, ok := t.bonds[bond]
	return ok
}

// Contrib 返回 n 张折算率为 rate 的债券折合的标准券额度：floor(n*rate/100)。
// 额度按券逐券向下取整后再求和，不得合并取整。
func Contrib(n, rate int64) int64 {
	return n * rate / 100
}
