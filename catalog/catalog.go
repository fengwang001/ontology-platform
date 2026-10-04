// Package catalog 维护医保目录类别、乙类先行自付比例与限价，
// 并按逐条取整的规则计算明细金额。
package catalog

import "errors"

const (
	// ClassA 甲类：无先行自付。
	ClassA = "甲"
	// ClassB 乙类：先行自付 ⌈b*p/100⌉，逐条向上取整。
	ClassB = "乙"
	// ClassC 丙类：全额先行自付，不进入合规额。
	ClassC = "丙"
)

// 金额参数上界（分）。
const maxMoney = 10_000_000_000_000

var (
	// ErrInvalidParam 参数非法。
	ErrInvalidParam = errors.New("catalog: invalid parameter")
	// ErrDuplicateCode 目录编码重复。
	ErrDuplicateCode = errors.New("catalog: duplicate item code")
	// ErrCodeNotFound 目录中不存在该编码。
	ErrCodeNotFound = errors.New("catalog: item code not found")
)

// Item 一条目录定义。
type Item struct {
	Code  string
	Class string
	// P 乙类先行自付百分数（1..99）；甲、丙恒为 0。
	P int
	// Limit 限价（分）；0 表示无限价。
	Limit int64
}

// Catalog 线程安全由 settle.Engine 的外层锁保证；
// 目录在结算运行中可追加，但为简化并发模型，所有调用与结算共用同一把锁。
type Catalog struct {
	items map[string]Item
	order []string
}

// New 创建空目录。
func New() *Catalog {
	return &Catalog{items: make(map[string]Item)}
}

// AddItem 追加目录项。
// p 为乙类先行自付百分数：乙类要求 1..99，甲/丙必须为 0。
// limit 为限价，0 表示无限价。
func (c *Catalog) AddItem(code, class string, p int, limit int64) error {
	if code == "" || (class != ClassA && class != ClassB && class != ClassC) {
		return ErrInvalidParam
	}
	if p < 0 || p > 100 || limit < 0 || limit > maxMoney {
		return ErrInvalidParam
	}
	switch class {
	case ClassB:
		if p < 1 || p > 99 {
			return ErrInvalidParam
		}
	default:
		if p != 0 {
			return ErrInvalidParam
		}
	}
	if _, ok := c.items[code]; ok {
		return ErrDuplicateCode
	}
	c.items[code] = Item{Code: code, Class: class, P: p, Limit: limit}
	c.order = append(c.order, code)
	return nil
}

// Get 返回目录项。
func (c *Catalog) Get(code string) (Item, bool) {
	it, ok := c.items[code]
	return it, ok
}

// Line 一条结算明细的输入。
type Line struct {
	Code     string
	Price    int64 // 单价（分），1..1e9
	Quantity int64 // 数量，1..1e4
}

// LineAmount 一条明细逐条计算后的金额分解。
type LineAmount struct {
	A int64 // 总额 = 单价*数量
	B int64 // 限价内金额
	C int64 // 先行自付（逐条取整）
	E int64 // 合规额 = b-c
}

// CalcLine 对单条明细做逐条计算，返回 (a, b, c, e)。
func (c *Catalog) CalcLine(line Line) (LineAmount, error) {
	if line.Price < 1 || line.Price > 1_000_000_000 ||
		line.Quantity < 1 || line.Quantity > 10_000 {
		return LineAmount{}, ErrInvalidParam
	}
	item, ok := c.items[line.Code]
	if !ok {
		return LineAmount{}, ErrCodeNotFound
	}
	a := line.Price * line.Quantity
	unit := line.Price
	if item.Limit > 0 && unit > item.Limit {
		unit = item.Limit
	}
	b := unit * line.Quantity
	var co int64
	switch item.Class {
	case ClassA:
		co = 0
	case ClassB:
		// ⌈b*p/100⌉ 逐条向上取整。
		co = (b*int64(item.P) + 99) / 100
	case ClassC:
		co = b
	}
	return LineAmount{A: a, B: b, C: co, E: b - co}, nil
}

// Calc 对一笔明细逐条计算并汇总 A、E；
// 缺失目录项时返回最小下标。
func (c *Catalog) Calc(lines []Line) (a, e int64, amounts []LineAmount, missingIndex int, err error) {
	if len(lines) < 1 || len(lines) > 200 {
		return 0, 0, nil, -1, ErrInvalidParam
	}
	amounts = make([]LineAmount, len(lines))
	for i, line := range lines {
		am, calcErr := c.CalcLine(line)
		if calcErr != nil {
			if errors.Is(calcErr, ErrCodeNotFound) {
				return 0, 0, nil, i, ErrCodeNotFound
			}
			return 0, 0, nil, -1, calcErr
		}
		amounts[i] = am
		a += am.A
		e += am.E
	}
	return a, e, amounts, -1, nil
}
