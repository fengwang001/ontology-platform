// Package modarith 提供 uint64 模素数域上的模乘、模加与平方-乘快速幂。
// 不依赖其他包。调用方保证 p >= 2 且 p 足够小使乘法不溢出。
package modarith

import (
	"math/big"
	"sync/atomic"
)

// lastMulCount 记录最近一次 Modpow 的模乘次数。
// 非导出，不出现在任何公开接口里；仅同包测试可白盒读取。
var lastMulCount atomic.Uint64

// Mul 返回 a*b mod p。
func Mul(a, b, p uint64) uint64 { return a * b % p }

// Add 返回 a+b mod p。
func Add(a, b, p uint64) uint64 { return (a + b) % p }

// Modpow 用平方-乘计算 base^e mod p：从 e 的最高位到最低位，
// 每位先 r = r*r mod p，若该位为 1 再 r = r*base mod p。
func Modpow(base uint64, e *big.Int, p uint64) uint64 {
	r := uint64(1) % p
	base %= p
	var n uint64
	for i := e.BitLen() - 1; i >= 0; i-- {
		r = Mul(r, r, p)
		n++
		if e.Bit(i) == 1 {
			r = Mul(r, base, p)
			n++
		}
	}
	lastMulCount.Store(n)
	return r
}
