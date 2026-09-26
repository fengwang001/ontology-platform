// Package gf256 实现 GF(2^8) 上的乘法，约化多项式
// x^8+x^4+x^3+x+1 = 0x11B。不依赖其他包。
package gf256

// Mul2 计算 2·a：左移 1 位，若原最高位为 1 则异或 0x1B 归约。
func Mul2(a byte) byte {
	b := a << 1
	if a&0x80 != 0 {
		b ^= 0x1B
	}
	return b
}

// Mul3 计算 3·a = 2·a ⊕ a。
func Mul3(a byte) byte {
	return Mul2(a) ^ a
}

// Mul 计算 GF(2^8) 上任意两元素的乘积（模 0x11B，逐位俄式乘法）。
func Mul(a, b byte) byte {
	var p byte
	for i := 0; i < 8; i++ {
		if b&1 != 0 {
			p ^= a
		}
		hi := a & 0x80
		a <<= 1
		if hi != 0 {
			a ^= 0x1B
		}
		b >>= 1
	}
	return p
}
