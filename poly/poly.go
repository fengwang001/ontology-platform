// Package poly 定义多项式系数切片与次数，小端约定：coeff[i] 是 x^i 的系数。
package poly

import "errors"

// MaxCoeff 是系数个数上限（次数上限 MaxCoeff-1）。
const MaxCoeff = 1 << 20

// ErrDegreeExceeded 在系数个数超过 MaxCoeff 时返回。
var ErrDegreeExceeded = errors.New("poly: coefficient count exceeds 1<<20")

// Poly 是不可变的多项式系数（小端）。构造时复制输入，之后任何方法都不写入，
// 因此可被多个 goroutine 并发只读。
type Poly struct {
	coeff []int64
}

// New 复制 coeff 构造 Poly；不做校验，超限由 Check 在求值前判定。
func New(coeff []int64) Poly {
	return Poly{coeff: append([]int64(nil), coeff...)}
}

// Degree 返回次数：len(coeff)-1；空多项式为 -1。
func (p Poly) Degree() int { return len(p.coeff) - 1 }

// Coeffs 返回内部系数切片，调用方不得修改。
func (p Poly) Coeffs() []int64 { return p.coeff }

// Check 校验次数上限。
func (p Poly) Check() error {
	if len(p.coeff) > MaxCoeff {
		return ErrDegreeExceeded
	}
	return nil
}
