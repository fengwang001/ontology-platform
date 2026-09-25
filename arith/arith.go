// Package arith 在 limb 幅值之上实现带符号整数的四则运算。
package arith

import (
	"sync/atomic"

	"ontology/limb"
)

// Int 是带符号整数：sign ∈ {-1,0,+1}，mag 为归一化幅值；零的 sign 恒为 0。
type Int struct {
	sign int
	mag  limb.Mag
}

// Make 归一化 m 并修正零的符号后构造 Int。
func Make(sign int, m limb.Mag) Int {
	m = limb.Norm(m)
	if len(m) == 0 {
		sign = 0
	}
	return Int{sign, m}
}

// Zero 返回 0。
func Zero() Int { return Int{} }

// Sign 返回 -1/0/+1。
func (a Int) Sign() int { return a.sign }

// Mag 返回幅值（调用方只读，不得修改）。
func (a Int) Mag() limb.Mag { return a.mag }

// Neg 返回 -a。
func Neg(a Int) Int { return Make(-a.sign, a.mag) }

// Add 返回 a+b。
func Add(a, b Int) Int {
	if a.sign == 0 {
		return b
	}
	if b.sign == 0 {
		return a
	}
	if a.sign == b.sign {
		return Make(a.sign, limb.Add(a.mag, b.mag))
	}
	switch limb.Cmp(a.mag, b.mag) {
	case 0:
		return Zero()
	case 1:
		return Make(a.sign, limb.Sub(a.mag, b.mag))
	default:
		return Make(b.sign, limb.Sub(b.mag, a.mag))
	}
}

// Sub 返回 a-b。
func Sub(a, b Int) Int { return Add(a, Neg(b)) }

// mulOps 记录最近一次 Mul 的标量乘累加次数；非导出，仅供包内测试核验。
var mulOps atomic.Int64

// Mul 返回 a*b，按 limb 逐位相乘（教科书竖式）。
func Mul(a, b Int) Int {
	if a.sign == 0 || b.sign == 0 {
		mulOps.Store(0)
		return Zero()
	}
	m := make(limb.Mag, len(a.mag)+len(b.mag))
	var ops int64
	for i, ai := range a.mag {
		var carry uint64
		for j, bj := range b.mag {
			cur := uint64(m[i+j]) + uint64(ai)*uint64(bj) + carry
			m[i+j] = uint32(cur % limb.Base)
			carry = cur / limb.Base
			ops++
		}
		m[i+len(b.mag)] = uint32(carry) // 该位置此前未被触碰，直接写入
	}
	mulOps.Store(ops)
	return Make(a.sign*b.sign, m)
}

// DivMod 返回向零截断的商 q 与余数 r：r=a-q*b，|r|<|b|，sign(r)==sign(a)。
// b 为零时 ok=false，不产生任何结果。
func DivMod(a, b Int) (q, r Int, ok bool) {
	if b.sign == 0 {
		return Zero(), Zero(), false
	}
	if a.sign == 0 {
		return Zero(), Zero(), true
	}
	qm, rm := divModMag(a.mag, b.mag)
	return Make(a.sign*b.sign, qm), Make(a.sign, rm), true
}

// divModMag 做无符号长除：每步从高位落下一位 limb，二分求商位。
func divModMag(a, b limb.Mag) (q, r limb.Mag) {
	if limb.Cmp(a, b) < 0 {
		return nil, a
	}
	q = make(limb.Mag, len(a))
	r = nil
	for i := len(a) - 1; i >= 0; i-- {
		r = limb.Norm(append(limb.Mag{a[i]}, r...)) // r = r*Base + a[i]
		lo, hi := uint32(0), uint32(limb.Base-1)
		for lo < hi { // 最大的 d 使 b*d <= r
			mid := lo + (hi-lo+1)/2
			if limb.Cmp(limb.MulSmall(b, mid), r) <= 0 {
				lo = mid
			} else {
				hi = mid - 1
			}
		}
		q[i] = lo
		if lo > 0 {
			r = limb.Sub(r, limb.MulSmall(b, lo))
		}
	}
	return limb.Norm(q), limb.Norm(r)
}
