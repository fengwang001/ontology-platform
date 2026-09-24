// Package dec 用精确算术生成 float64 的最短往返十进制有效数字。
package dec

import (
	"math"
	"math/big"
	"sync/atomic"

	"ontology/bits"
)

// checks 是非导出的诊断计数器：精确往返检查的执行次数。
var checks atomic.Int64

// Checks 返回精确往返检查累计执行次数。
func Checks() int64 { return checks.Load() }

// ResetChecks 清零检查计数器。
func ResetChecks() { checks.Store(0) }

var pow10tab [400]*big.Int

func init() {
	pow10tab[0] = big.NewInt(1)
	for i := 1; i < len(pow10tab); i++ {
		pow10tab[i] = new(big.Int).Mul(pow10tab[i-1], big.NewInt(10))
	}
}

func pow10(n int) *big.Int { return pow10tab[n] }

// Shortest 返回有限非零 v 的最短往返十进制有效数字 digits 与指数 exp，
// 满足 |v| = digits(作为整数) × 10^exp，digits 无首尾零。符号由调用方处理。
func Shortest(v float64) (digits string, exp int) {
	p := bits.Split(v)
	if p.Kind == bits.Zero {
		return "0", 0
	}
	m, e2 := p.Mant, p.Exp
	var rn, rd big.Int
	rn.SetUint64(m)
	rd.SetUint64(1)
	if e2 >= 0 {
		rn.Lsh(&rn, uint(e2))
	} else {
		rd.Lsh(&rd, uint(-e2))
	}
	e10 := floorLog10(&rn, &rd, math.Abs(v))
	target := math.Abs(v)
	for k := 1; k <= 17; k++ {
		s := k - 1 - e10 // t = |v| × 10^s ∈ [10^(k-1), 10^k)
		var tn, td big.Int
		if s >= 0 {
			tn.Mul(&rn, pow10(s))
			td.Set(&rd)
		} else {
			tn.Set(&rn)
			td.Mul(&rd, pow10(-s))
		}
		var n0, rem, twice big.Int
		n0.QuoRem(&tn, &td, &rem)
		up := new(big.Int).Add(&n0, big.NewInt(1))
		first, second := &n0, up
		if twice.Lsh(&rem, 1).Cmp(&td) > 0 {
			first, second = second, first // 较近者先试
		}
		for _, n := range []*big.Int{first, second} {
			if rem.Sign() == 0 && n != first {
				break
			}
			checks.Add(1)
			if roundtrips(n, s, target) {
				return normalize(n, -s)
			}
		}
	}
	panic("dec: 17 位仍无法往返，不可能发生")
}

// roundtrips 精确检查候选 n × 10^(-s) 解回是否等于 target。
func roundtrips(n *big.Int, s int, target float64) bool {
	var cn, cd big.Int
	if s >= 0 {
		cn.Set(n)
		cd.Set(pow10(s))
	} else {
		cn.Mul(n, pow10(-s))
		cd.SetUint64(1)
	}
	return ratToFloat64(false, &cn, &cd) == target
}

// normalize 去掉十进制串尾随零并相应调整指数。
func normalize(n *big.Int, exp int) (string, int) {
	d := n.String()
	for len(d) > 1 && d[len(d)-1] == '0' {
		d = d[:len(d)-1]
		exp++
	}
	return d, exp
}

// floorLog10 返回满足 10^e <= rn/rd < 10^(e+1) 的 e，absv 用于浮点估值。
func floorLog10(rn, rd *big.Int, absv float64) int {
	e := int(math.Floor(math.Log10(absv)))
	for cmpPow10(rn, rd, e) < 0 {
		e--
	}
	for cmpPow10(rn, rd, e+1) >= 0 {
		e++
	}
	return e
}

// cmpPow10 比较 rn/rd 与 10^e，返回 -1/0/+1。
func cmpPow10(rn, rd *big.Int, e int) int {
	var a, b big.Int
	if e >= 0 {
		a.Set(rn)
		b.Mul(rd, pow10(e))
	} else {
		a.Mul(rn, pow10(-e))
		b.Set(rd)
	}
	return a.Cmp(&b)
}

// RatToFloat64 把正有理数 r 按最近舍入（恰半取偶）转为 float64，neg 控制符号。
// 上溢返回 ±Inf，下溢返回 ±0。
func RatToFloat64(neg bool, r *big.Rat) float64 {
	return ratToFloat64(neg, r.Num(), r.Denom())
}

// ratToFloat64 是 RatToFloat64 的分子/分母形式，不修改入参。
func ratToFloat64(neg bool, p, q *big.Int) float64 {
	var out uint64
	if neg {
		out = 1 << 63
	}
	if p.Sign() == 0 {
		return math.Float64frombits(out)
	}
	e := p.BitLen() - q.BitLen() // 之后修正为 2^e <= p/q < 2^(e+1)
	var t big.Int
	if e >= 0 {
		t.Lsh(q, uint(e))
		if p.Cmp(&t) < 0 {
			e--
		}
	} else {
		t.Lsh(p, uint(-e))
		if t.Cmp(q) < 0 {
			e--
		}
	}
	if e > 1023 {
		return math.Float64frombits(out | 0x7FF<<52)
	}
	if e >= -1022 { // 正规数：53 位尾数
		var m *big.Int
		if shift := e - 52; shift >= 0 {
			t.Lsh(q, uint(shift))
			m = roundQuo(p, &t)
		} else {
			t.Lsh(p, uint(-shift))
			m = roundQuo(&t, q)
		}
		if m.Bit(53) == 1 { // 舍入进位
			m.Rsh(m, 1)
			e++
			if e > 1023 {
				return math.Float64frombits(out | 0x7FF<<52)
			}
		}
		out |= uint64(e+1023)<<52 | m.Uint64()&(1<<52-1)
	} else { // 次正规数：尾数即 round(p/q × 2^1074)
		t.Lsh(p, 1074)
		out |= roundQuo(&t, q).Uint64()
	}
	return math.Float64frombits(out)
}

// roundQuo 返回 num/den 四舍五入（恰半取偶）的整数商，不修改入参。
func roundQuo(num, den *big.Int) *big.Int {
	q, r := new(big.Int), new(big.Int)
	q.QuoRem(num, den, r)
	r.Lsh(r, 1)
	if c := r.Cmp(den); c > 0 || (c == 0 && q.Bit(0) == 1) {
		q.Add(q, big.NewInt(1))
	}
	return q
}
