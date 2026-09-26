// Package gf256 implements multiplication in GF(2^8) with the AES
// reduction polynomial x^8+x^4+x^3+x+1 (0x11B).
package gf256

// Mul2 returns 2·a (xtime): (a<<1) reduced by 0x1B when the high bit was set.
func Mul2(a byte) byte {
	r := a << 1
	if a&0x80 != 0 {
		r ^= 0x1b
	}
	return r
}

// Mul3 returns 3·a = 2·a ⊕ a.
func Mul3(a byte) byte { return Mul2(a) ^ a }

// Mul returns a·b in GF(2^8) via shift-and-add over the field.
func Mul(a, b byte) byte {
	var p byte
	for i := 0; i < 8; i++ {
		if b&1 != 0 {
			p ^= a
		}
		hi := a & 0x80
		a <<= 1
		if hi != 0 {
			a ^= 0x1b
		}
		b >>= 1
	}
	return p
}
