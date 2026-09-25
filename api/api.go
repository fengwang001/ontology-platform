// Package api 是对外接口：构造、求值、次数、自检。
package api

import (
	"errors"
	"math"
	"math/big"

	"ontology/eval"
	"ontology/poly"
)

// 对外暴露的三类可判定哨兵错误，互不相同。
var (
	ErrMulOverflow    = eval.ErrMulOverflow
	ErrAddOverflow    = eval.ErrAddOverflow
	ErrDegreeExceeded = poly.ErrDegreeExceeded
)

// Poly 是构造后不可变的多项式，方法可被多 goroutine 并发调用。
type Poly struct {
	p poly.Poly
}

// New 以小端约定构造多项式：coeff[i] 是 x^i 的系数。
func New(coeff []int64) *Poly { return &Poly{p: poly.New(coeff)} }

// Degree 返回次数 len(coeff)-1；空多项式为 -1。
func (p *Poly) Degree() int { return p.p.Degree() }

// Eval 用 Horner 法精确求值，溢出或超限时返回 0 与可判定哨兵错误。
func (p *Poly) Eval(x int64) (int64, error) { return eval.Eval(p.p, x) }

// bigRef 是朴素参照：逐项 coeff[i]*x^i 用 big.Int 精确累加。
func bigRef(coeff []int64, x int64) *big.Int {
	total := new(big.Int)
	bx := big.NewInt(x)
	pow := big.NewInt(1)
	for _, c := range coeff {
		total.Add(total, new(big.Int).Mul(big.NewInt(c), pow))
		pow.Mul(pow, bx)
	}
	return total
}

// SelfCheck 对一组内置多项式核验四条不变量，全部通过返回 nil。
func (p *Poly) SelfCheck() error {
	cases := []struct {
		coeff []int64
		x     int64
	}{
		{[]int64{1, 2, 3}, 2},        // 17
		{[]int64{5, -2, 1}, 3},       // 8
		{[]int64{0, 0, 1}, 10},       // 100
		{[]int64{1, 1, 1, 1}, -2},    // -5
		{[]int64{1, 0, 1}, 67108864}, // 4503599627370497
		{nil, 5},                     // 空多项式 = 0
		{[]int64{0, 0, 0}, 7},        // 全零 = 0
		{[]int64{1}, 999},            // 常数 = 1
		{[]int64{-9}, 0},             // 常数与 x 无关
		{[]int64{7, -3, 11, -13, 2}, 6},
	}
	for _, c := range cases {
		got, err := New(c.coeff).Eval(c.x)
		if err != nil {
			return err
		}
		want := bigRef(c.coeff, c.x)
		if !want.IsInt64() || want.Int64() != got {
			return errors.New("api: selfcheck mismatch vs big reference")
		}
	}
	// 不变量 4：三类错误可判定且互不相同，被拒后返回 0。
	if errors.Is(ErrMulOverflow, ErrAddOverflow) ||
		errors.Is(ErrMulOverflow, ErrDegreeExceeded) ||
		errors.Is(ErrAddOverflow, ErrDegreeExceeded) {
		return errors.New("api: sentinel errors not distinct")
	}
	if v, err := New([]int64{0, 3037000500}).Eval(3037000500); !errors.Is(err, ErrMulOverflow) || v != 0 {
		return errors.New("api: mul overflow not detected cleanly")
	}
	if v, err := New([]int64{math.MaxInt64, 1}).Eval(1); !errors.Is(err, ErrAddOverflow) || v != 0 {
		return errors.New("api: add overflow not detected cleanly")
	}
	return nil
}
