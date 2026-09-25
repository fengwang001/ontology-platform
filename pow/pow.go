// Package pow 实现模幂（快速幂取模），依赖 mod 包。
package pow

import (
	"errors"
	"sync/atomic"

	"ontology/mod"
)

// 三类可判定的哨兵错误，互不相同。
var (
	ErrZeroModulus      = errors.New("pow: modulus is zero")
	ErrNegativeModulus  = errors.New("pow: modulus is negative")
	ErrNegativeExponent = errors.New("pow: exponent is negative")
)

// lastMulModCalls 记录最近一次 PowMod 执行的 mulmod 次数。
// 非导出，不出现在任何公开接口；仅供包内白盒测试读取。
var lastMulModCalls atomic.Int64

// PowMod 计算 base^exp mod mod，结果恒为非负且严格小于 mod。
// 平方乘；所有乘法一律走 mod.MulMod，不做直接 int64 相乘。
func PowMod(base, exp, modulus int64) (int64, error) {
	if modulus == 0 {
		return 0, ErrZeroModulus
	}
	if modulus < 0 {
		return 0, ErrNegativeModulus
	}
	if exp < 0 {
		return 0, ErrNegativeExponent
	}
	var calls int64
	mul := func(a, b int64) int64 {
		calls++
		return mod.MulMod(a, b, modulus)
	}
	b := mod.Normalize(base, modulus) // 负 base 在此归一化到 [0, mod)
	r := int64(1) % modulus           // mod==1 时 r=0，天然覆盖 exp==0 边界
	for e := exp; e > 0; e >>= 1 {
		if e&1 == 1 {
			r = mul(r, b)
		}
		if e > 1 {
			b = mul(b, b)
		}
	}
	lastMulModCalls.Store(calls)
	return r, nil
}
