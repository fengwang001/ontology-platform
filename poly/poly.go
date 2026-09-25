// Package poly 提供 GF(2) 上多项式「移位 + 归约」的乘法原语。
package poly

import "errors"

// ErrInvalidPolynomial 表示给定的字节不能构成 8 次不可约归约多项式。
var ErrInvalidPolynomial = errors.New("poly: not an irreducible degree-8 polynomial")

// Poly 是不可约多项式 x^8 + low（low 为低 8 位系数）。
type Poly struct {
	mod uint16 // 0x100 | low
}

// GF(2) 上次数 1..4 的全部不可约多项式；8 次多项式可约当且仅当含次数 ≤4 的不可约因子。
var irreducibles = [...]uint16{0x2, 0x3, 0x7, 0xB, 0xD, 0x13, 0x19, 0x1F}

// New 校验 x^8+low 不可约后构建归约多项式；不可约性不满足时返回 ErrInvalidPolynomial。
func New(low uint8) (Poly, error) {
	p := Poly{mod: 0x100 | uint16(low)}
	for _, d := range irreducibles {
		if pmod(p.mod, d) == 0 {
			return Poly{}, ErrInvalidPolynomial
		}
	}
	return p, nil
}

// pmod 计算多项式 a 模 d（GF(2) 系数，按最高位对齐异或消元）。
func pmod(a, d uint16) uint16 {
	for deg(a) >= deg(d) {
		a ^= d << (deg(a) - deg(d))
	}
	return a
}

// deg 返回多项式的次数加一（0 返回 0），即最高有效位位置。
func deg(x uint16) (n uint16) {
	for x != 0 {
		n++
		x >>= 1
	}
	return n
}

// Mul 把 a、b 视为次数 ≤7 的多项式相乘，再对 p 归约回 8 位。
func (p Poly) Mul(a, b uint8) uint8 {
	r := 0
	aa := uint16(a)
	bb := uint16(b)
	for bb != 0 {
		if bb&1 != 0 {
			r ^= int(aa)
		}
		bb >>= 1
		aa <<= 1
		if aa&0x100 != 0 {
			aa ^= p.mod
		}
	}
	return uint8(r)
}
