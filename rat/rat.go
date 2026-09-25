// Package rat 提供不可变有理数（分数）类型与规范化四则运算。
// 不变量：d > 0、gcd(|n|, d) == 1、符号只在分子上、0 恒为 0/1。
// 依赖 num，不反向依赖。
package rat

import (
	"strconv"

	"ontology/num"
)

// Rat 是既约分数 n/d。构造后不可变，只读方法可并发调用。
type Rat struct {
	n, d int64
}

// New 构造 n/d 的规范形式。d == 0 报 num.ErrZeroDenominator。
func New(n, d int64) (*Rat, error) {
	rn, rd, err := num.Normalize(n, d)
	if err != nil {
		return nil, err
	}
	return &Rat{rn, rd}, nil
}

// Num 返回分子（符号只在分子上）。
func (r *Rat) Num() int64 { return r.n }

// Den 返回分母（恒为正）。
func (r *Rat) Den() int64 { return r.d }

// IsZero 报告值是否为 0（规范形式 0/1）。
func (r *Rat) IsZero() bool { return r.n == 0 }

// String 输出 "n/d"，n==0 时为 "0/1"，分母恒正。
func (r *Rat) String() string {
	return strconv.FormatInt(r.n, 10) + "/" + strconv.FormatInt(r.d, 10)
}

// Add 返回 a+b：lcm 通分后相加再约分。任何中间溢出报 num.ErrAddOverflow。
func Add(a, b *Rat) (*Rat, error) {
	g := num.Gcd(a.d, b.d)
	l, err := num.Mul64(a.d/g, b.d) // lcm(a.d, b.d)
	if err != nil {
		return nil, num.ErrAddOverflow
	}
	t1, err := num.Mul64(a.n, l/a.d)
	if err != nil {
		return nil, num.ErrAddOverflow
	}
	t2, err := num.Mul64(b.n, l/b.d)
	if err != nil {
		return nil, num.ErrAddOverflow
	}
	n, err := num.Add64(t1, t2)
	if err != nil {
		return nil, err
	}
	return New(n, l)
}

// Sub 返回 a-b：与 Add 同法，分子相减。溢出报 num.ErrAddOverflow。
func Sub(a, b *Rat) (*Rat, error) {
	g := num.Gcd(a.d, b.d)
	l, err := num.Mul64(a.d/g, b.d)
	if err != nil {
		return nil, num.ErrAddOverflow
	}
	t1, err := num.Mul64(a.n, l/a.d)
	if err != nil {
		return nil, num.ErrAddOverflow
	}
	t2, err := num.Mul64(b.n, l/b.d)
	if err != nil {
		return nil, num.ErrAddOverflow
	}
	n, err := num.Sub64(t1, t2)
	if err != nil {
		return nil, err
	}
	return New(n, l)
}

// Mul 返回 a*b：先交叉约分再相乘。溢出报 num.ErrMulOverflow。
func Mul(a, b *Rat) (*Rat, error) {
	g1 := num.Gcd(a.n, b.d)
	g2 := num.Gcd(b.n, a.d)
	n, err := num.Mul64(a.n/g1, b.n/g2)
	if err != nil {
		return nil, err
	}
	d, err := num.Mul64(a.d/g2, b.d/g1)
	if err != nil {
		return nil, err
	}
	return New(n, d)
}

// Div 返回 a/b。b 为零报 num.ErrZeroDenominator；商无法表示时报
// num.ErrNormOverflow；乘法溢出报 num.ErrMulOverflow。
func Div(a, b *Rat) (*Rat, error) {
	if b.n == 0 {
		return nil, num.ErrZeroDenominator
	}
	rec, err := New(b.d, b.n) // 倒数，符号归一由 Normalize 完成
	if err != nil {
		return nil, err
	}
	return Mul(a, rec)
}
