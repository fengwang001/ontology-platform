// Package refund 计算累计应退并管理每张授权单的手续费欠额。
package refund

import (
	"errors"
	"math/bits"
)

// ErrInvalid 表示构造参数非法（beta 超出 0..100 或 fee 超出 0..1e9）。
var ErrInvalid = errors.New("refund: invalid argument")

// Calculator 持有 B 级系数 β、统一手续费 fee 与每单欠额账。
// 本类型不自带锁；所有调用均在 rma.Store 的事务（已持锁）内发生，
// 避免双锁顺序问题。
type Calculator struct {
	beta int64
	fee  int64
	owe  map[string]int64 // 授权单 id -> 剩余欠额；到期单 Void 后删除
}

// New 构造退款计算器。beta 为 0..100 的百分数，fee 为 0..1e9 分。
func New(beta, fee int64) (*Calculator, error) {
	if beta < 0 || beta > 100 || fee < 0 || fee > 1_000_000_000 {
		return nil, ErrInvalid
	}
	return &Calculator{beta: beta, fee: fee, owe: map[string]int64{}}, nil
}

// Open 为新授权单开立手续费欠额（初始 owe=fee）。重复开立视为冲突由上层保证。
func (c *Calculator) Open(id string) { c.owe[id] = c.fee }

// Owe 返回某授权单当前剩余欠额；到期/未知单返回 0。
func (c *Calculator) Owe(id string) int64 { return c.owe[id] }

// Void 使到期授权单未扣完的欠额作废。
func (c *Calculator) Void(id string) { delete(c.owe, id) }

// div128 计算 floor((hi<<64+lo)/d)。本题分子 ≤1e20、d≥100，商必 ≤1e12<2^40。
// 把 128 位被除数按 32 位切成 4 个词（高到低 w0..w3），做学校长除法，
// 每次 (余数<<32 | 词) / d 得一个 32 位商词；d<2^30 保证每步余数<<32<2^62。
func div128(hi, lo, d uint64) uint64 {
	w := [4]uint64{hi >> 32, hi & 0xffffffff, lo >> 32, lo & 0xffffffff}
	var rem, qHi, qLo uint64
	for i := 0; i < 4; i++ {
		// 窗口 x = rem*2^32 + w[i] < d*2^32 < 2^59，单个 64 位字即可容纳，
		// 直接 Div64(0, x, d) 得 32 位商词与新余数（rem 恒 < d）。
		q, r := bits.Div64(0, rem<<32|w[i], d)
		if i < 2 {
			qHi = qHi<<32 | q
		} else {
			qLo = qLo<<32 | q
		}
		rem = r
	}
	if qHi != 0 {
		panic("refund: quotient overflow")
	}
	return qLo
}

// Cumulative 返回 R = floor(paid*(100*a+beta*b)/(100*shipped))。
// 中间乘积最大 1e12 * (100*1e6) = 1e20，按 128 位计算。
func (c *Calculator) Cumulative(paid, shipped, a, b int64) int64 {
	weight := uint64(100*a) + uint64(c.beta)*uint64(b) // ≤ 1e8+1e2*1e6 ≈ 2e8
	hi, lo := bits.Mul64(uint64(paid), weight)         // 128 位分子
	d := uint64(100) * uint64(shipped)
	return int64(div128(hi, lo, d))
}

// Settle 为某次应退 x 结算手续费：d=min(x, owe)，返回扣费 d 与实付 x-d。
// 欠额跨该授权单的多次收货延续。
func (c *Calculator) Settle(id string, x int64) (fee, paid int64) {
	owe := c.owe[id]
	d := x
	if d > owe {
		d = owe
	}
	c.owe[id] = owe - d
	return d, x - d
}
