// Package dec 用精确有理数算术生成 float64 的最短可往返十进制有效数字。
package dec

import (
	"errors"
	"math"
	"math/big"
	"sync/atomic"

	"ontology/bits"
)

// Decimal 是一个非零有限十进制值：(-1)^Neg * 0.Digits * 10^(Exp+1)。
// Digits 为有效数字串，无尾部零，至少一位。
type Decimal struct {
	Neg    bool
	Digits string
	Exp    int // 最高有效数字的十进制指数
}

// ErrNonFinite 表示输入为 NaN 或 Inf，无法给出十进制表示。
var ErrNonFinite = errors.New("dec: NaN 或 Inf 没有十进制表示")

var checks atomic.Int64

// Checks 返回精确往返检查累计执行次数（仅诊断用途）。
func Checks() int64 { return checks.Load() }

// ResetChecks 将诊断计数器清零。
func ResetChecks() { checks.Store(0) }

var pow10 = func() []*big.Int {
	t := make([]*big.Int, 401)
	t[0] = big.NewInt(1)
	for i := 1; i < len(t); i++ {
		t[i] = new(big.Int).Mul(t[i-1], big.NewInt(10))
	}
	return t
}()

func ratPow10(k int) *big.Rat {
	if k >= 0 {
		return new(big.Rat).SetInt(pow10[k])
	}
	return new(big.Rat).SetFrac(big.NewInt(1), pow10[-k])
}

func ratPow2(m uint64, e int) *big.Rat {
	i := new(big.Int).SetUint64(m)
	if e >= 0 {
		return new(big.Rat).SetInt(i.Lsh(i, uint(e)))
	}
	d := new(big.Int).Lsh(big.NewInt(1), uint(-e))
	return new(big.Rat).SetFrac(i, d)
}

// interval 返回 x 的精确舍入区间端点及开闭性（ties-to-even 时端点归 x）。
func interval(x float64) (lo, hi *big.Rat, loClosed, hiClosed bool) {
	p := bits.Decompose(x)
	_, m, e := bits.Int(x)
	closed := m%2 == 0
	hi = ratPow2(2*m+1, e-1)
	if m == 1<<52 && p.Exp > 1 {
		lo = ratPow2(4*m-1, e-2) // 2 的幂：下侧间隔减半
	} else {
		lo = ratPow2(2*m-1, e-1)
	}
	return lo, hi, closed, closed
}

// floorRat 返回正有理数 r 的下取整。
func floorRat(r *big.Rat) *big.Int {
	q, _ := new(big.Int).QuoRem(r.Num(), r.Denom(), new(big.Int))
	return q
}

// roundRatInt 把正有理数 r 舍入到最近整数，平局取偶。
func roundRatInt(r *big.Rat) *big.Int {
	q, rem := new(big.Int).QuoRem(r.Num(), r.Denom(), new(big.Int))
	c := new(big.Int).Lsh(rem, 1).Cmp(r.Denom())
	if c > 0 || c == 0 && q.Bit(0) == 1 {
		q.Add(q, big.NewInt(1))
	}
	return q
}

// bounds 返回落在区间内的步长整数倍的下标范围 [dMin, dMax]。
func bounds(lo, hi *big.Rat, loC, hiC bool, step *big.Rat) (dMin, dMax *big.Int) {
	ql := new(big.Rat).Quo(lo, step)
	fl := floorRat(ql)
	dMin = new(big.Int).Add(fl, big.NewInt(1))
	if loC && new(big.Rat).SetInt(fl).Cmp(ql) == 0 {
		dMin = fl
	}
	qh := new(big.Rat).Quo(hi, step)
	fh := floorRat(qh)
	dMax = fh
	if !hiC && new(big.Rat).SetInt(fh).Cmp(qh) == 0 {
		dMax = new(big.Int).Sub(fh, big.NewInt(1))
	}
	return dMin, dMax
}

// Shortest 返回 x 的最短可往返十进制表示。x 必须有限且非零。
func Shortest(x float64) (Decimal, error) {
	if bits.IsNaN(x) || bits.IsInf(x) {
		return Decimal{}, ErrNonFinite
	}
	if bits.IsZero(x) {
		return Decimal{}, errors.New("dec: 零没有有效数字")
	}
	neg, _, _ := bits.Int(x)
	v := ratPow2Abs(x)
	lo, hi, loC, hiC := interval(x)
	abs := math.Abs(x)
	for n := 1; n <= 17; n++ {
		checks.Add(1)
		k := int(math.Floor(math.Log10(abs))) - n + 1
		for v.Cmp(ratPow10(n-1+k)) < 0 {
			k--
		}
		for v.Cmp(ratPow10(n+k)) >= 0 {
			k++
		}
		step := ratPow10(k)
		dMin, dMax := bounds(lo, hi, loC, hiC, step)
		if dMin.Cmp(dMax) > 0 {
			continue
		}
		d := roundRatInt(new(big.Rat).Quo(v, step))
		if d.Cmp(dMin) < 0 {
			d = dMin
		} else if d.Cmp(dMax) > 0 {
			d = dMax
		}
		s := d.String()
		for len(s) > 1 && s[len(s)-1] == '0' {
			s = s[:len(s)-1]
			k++
		}
		return Decimal{Neg: neg, Digits: s, Exp: k + len(s) - 1}, nil
	}
	return Decimal{}, errors.New("dec: 17 位仍无法往返（不应发生）")
}

func ratPow2Abs(x float64) *big.Rat {
	_, m, e := bits.Int(x)
	return ratPow2(m, e)
}
