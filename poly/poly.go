// Package poly 提供 GF(2) 上次数 ≤14 多项式「移位 + 归约」原语：
// 对给定的归约多项式字节 mod（表示 x^8 + mod 的低位）做单次多项式乘法归约。
package poly

import "errors"

// ErrNotIrreducible 表示给定字节不能构成 8 次不可约归约多项式。
var ErrNotIrreducible = errors.New("poly: not an irreducible reduction polynomial")

// Reducer 固定一个归约多项式 x^8+mod，做多项式乘后归约。
type Reducer struct{ mod uint8 }

// NewReducer 校验 x^8+mod 在 GF(2) 上不可约（无 1..4 次因子），否则报 ErrNotIrreducible。
func NewReducer(mod uint8) (*Reducer, error) {
	p := 0x100 | int(mod)
	for deg := 1; deg <= 4; deg++ {
		for low := 0; low < 1<<deg; low++ {
			if pmod(p, 1<<deg|low) == 0 {
				return nil, ErrNotIrreducible
			}
		}
	}
	return &Reducer{mod: mod}, nil
}

// Mul 把 a、b 视为 GF(2) 上次数 ≤7 的多项式，相乘后对 x^8+mod 归约回 8 位。
func (r *Reducer) Mul(a, b uint8) uint8 {
	p, aa, bb := 0, int(a), int(b)
	for bb != 0 {
		if bb&1 != 0 {
			p ^= aa
		}
		aa <<= 1
		bb >>= 1
	}
	return uint8(pmod(p, 0x100|int(r.mod)))
}

// deg 返回多项式次数；零多项式返回 -1。
func deg(x int) int {
	d := -1
	for x != 0 {
		x >>= 1
		d++
	}
	return d
}

// pmod 返回 p 模 d 的余式（GF(2) 多项式除法）。
func pmod(p, d int) int {
	for dd := deg(d); deg(p) >= dd; {
		p ^= d << (deg(p) - dd)
	}
	return p
}
