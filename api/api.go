// Package api 对外提供多项式求值接口。
package api

import (
	"math"
	"math/big"

	"ontology/eval"
	"ontology/poly"
)

// Poly 是构造后只读的多项式，可被多 goroutine 并发求值。
type Poly struct {
	coeff poly.Coeffs
}

// New 构造多项式，coeff 小端存放（coeff[i] 是 x^i 的系数）；内部持有副本。
func New(coeff []int64) *Poly {
	return &Poly{coeff: poly.Clone(coeff)}
}

// Eval 用 Horner 法计算 Σ coeff[i]·x^i，int64 精确并检出溢出。
func (p *Poly) Eval(x int64) (int64, error) {
	return eval.Eval(p.coeff, x)
}

// Degree 返回次数 len(coeff)-1；零多项式为 -1。
func (p *Poly) Degree() int {
	return poly.Degree(p.coeff)
}

// SelfCheck 对一组内置多项式核验四条不变量，全部通过才返回 true。
func (p *Poly) SelfCheck() bool {
	return checkBigReference() && checkTermwise() && checkEmptyConst() &&
		checkDistinctErrors() && eval.SelfCheck()
}

// checkBigReference 不变量1：无溢出时 Eval 与 big 精确逐项累加一致。
func checkBigReference() bool {
	cases := []struct {
		coeff []int64
		x     int64
	}{
		{[]int64{1, 2, 3}, 2}, {[]int64{5, -2, 1}, 3}, {[]int64{0, 0, 1}, 10},
		{[]int64{1, 1, 1, 1}, -2}, {[]int64{1, 0, 1}, 67108864},
		{[]int64{7, -3, 9, -4, 2}, 5}, {[]int64{-8, 6}, -7},
	}
	for _, c := range cases {
		got, err := New(c.coeff).Eval(c.x)
		if err != nil || got != bigEval(c.coeff, c.x) {
			return false
		}
	}
	return true
}

// bigEval 用 math/big 逐项 coeff[i]*x^i 精确累加，作为参照。
func bigEval(coeff []int64, x int64) int64 {
	total := new(big.Int)
	bx := big.NewInt(x)
	for i, c := range coeff {
		term := new(big.Int).Exp(bx, big.NewInt(int64(i)), nil)
		term.Mul(term, big.NewInt(c))
		total.Add(total, term)
	}
	return total.Int64()
}

// checkTermwise 不变量2：无溢出时 Horner 与「先算 x^i 再逐项求和」一致。
func checkTermwise() bool {
	cases := []struct {
		coeff []int64
		x     int64
	}{
		{[]int64{1, 2, 3}, 2}, {[]int64{5, -2, 1}, 3}, {[]int64{1, 1, 1, 1}, -2},
		{[]int64{3, 0, -5, 2}, 4}, {[]int64{9}, 100},
	}
	for _, c := range cases {
		got, err := New(c.coeff).Eval(c.x)
		if err != nil || got != termwise(c.coeff, c.x) {
			return false
		}
	}
	return true
}

// termwise 先算 x^i 再逐项求和（小范围，不溢出）。
func termwise(coeff []int64, x int64) int64 {
	sum, pow := int64(0), int64(1)
	for _, c := range coeff {
		sum += c * pow
		pow *= x
	}
	return sum
}

// checkEmptyConst 不变量3：空/全零多项式为 0，常数多项式与 x 无关。
func checkEmptyConst() bool {
	for _, x := range []int64{0, 1, -7, 999} {
		if v, err := New(nil).Eval(x); err != nil || v != 0 {
			return false
		}
		if v, err := New([]int64{0, 0, 0}).Eval(x); err != nil || v != 0 {
			return false
		}
		if v, err := New([]int64{42}).Eval(x); err != nil || v != 42 {
			return false
		}
	}
	return true
}

// checkDistinctErrors 不变量4：三类错误互不相同、可判定，被拒后不返回半成品。
func checkDistinctErrors() bool {
	if _, err := New([]int64{0, 3037000500}).Eval(3037000500); err != eval.ErrMulOverflow {
		return false
	}
	if _, err := New([]int64{1, 1}).Eval(math.MaxInt64); err != eval.ErrAddOverflow {
		return false
	}
	if _, err := New(make([]int64, (1<<20)+1)).Eval(1); err != eval.ErrDegreeLimit {
		return false
	}
	if eval.ErrMulOverflow == eval.ErrAddOverflow ||
		eval.ErrMulOverflow == eval.ErrDegreeLimit ||
		eval.ErrAddOverflow == eval.ErrDegreeLimit {
		return false
	}
	return true
}
