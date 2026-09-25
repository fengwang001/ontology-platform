// Package hash 表示并求值 MinHash 用的线性同余哈希 h(x) = (a*x + b) mod p。
// 参数由调用方注入，包内不含任何随机源。
package hash

import (
	"errors"
	"fmt"
	"math/big"
)

// ErrBadParams 表示哈希参数非法：p <= 1 或 a ≡ 0 (mod p)（哈希退化）。
var ErrBadParams = errors.New("hash: invalid parameters (need p > 1 and a mod p != 0)")

// Hash 是一个确定性哈希函数 h(x) = (a*x + b) mod p。
type Hash struct {
	a, b, p uint64
}

// New 构造哈希函数；p <= 1 或 a ≡ 0 (mod p) 时返回 ErrBadParams，不产生任何状态。
func New(a, b, p uint64) (Hash, error) {
	if p <= 1 || a%p == 0 {
		return Hash{}, ErrBadParams
	}
	return Hash{a: a, b: b, p: p}, nil
}

// Eval 计算 (a*x + b) mod p。用 big.Int 避免 a*x 溢出 uint64，保证确定性。
func (h Hash) Eval(x uint64) uint64 {
	var v big.Int
	v.Mul(new(big.Int).SetUint64(h.a), new(big.Int).SetUint64(x))
	v.Add(&v, new(big.Int).SetUint64(h.b))
	v.Mod(&v, new(big.Int).SetUint64(h.p))
	return v.Uint64()
}

// Params 返回构造参数，用于比较两个 sketch 的哈希函数集是否一致。
func (h Hash) Params() (a, b, p uint64) { return h.a, h.b, h.p }

// String 便于调试输出。
func (h Hash) String() string { return fmt.Sprintf("(%d*x+%d) mod %d", h.a, h.b, h.p) }
