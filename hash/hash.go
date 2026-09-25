// Package hash 表示可注入的仿射哈希函数 h(x) = (a*x + b) mod p。
package hash

import "errors"

// ErrInvalidParams 表示哈希参数非法：p <= 1 或 a ≡ 0 (mod p)。
var ErrInvalidParams = errors.New("hash: invalid params (need p > 1 and a % p != 0)")

// Hash 是可注入的哈希函数。Params 返回构造参数，用于比较两个
// sketch 的哈希函数集是否一致。
type Hash interface {
	Eval(x uint64) uint64
	Params() (a, b, p uint64)
}

type affine struct{ a, b, p uint64 }

// New 构造 h(x) = (a*x + b) mod p，参数非法时返回 ErrInvalidParams。
func New(a, b, p uint64) (Hash, error) {
	h := affine{a, b, p}
	if err := Validate(h); err != nil {
		return nil, err
	}
	return h, nil
}

// Validate 校验任一 Hash 的参数是否合法（库内不自建随机，参数全由调用方给定）。
func Validate(h Hash) error {
	a, _, p := h.Params()
	if p <= 1 || a%p == 0 {
		return ErrInvalidParams
	}
	return nil
}

func (h affine) Eval(x uint64) uint64 { return (h.a*x + h.b) % h.p }

func (h affine) Params() (a, b, p uint64) { return h.a, h.b, h.p }
