package bloom

import (
	"errors"
	"math"
)

// 参数推导相关错误，使用 errors.Is 即可判定。
var (
	// ErrInvalidN 表示目标元素数 n 不合法（n <= 0）。
	ErrInvalidN = errors.New("bloom: expected number of elements n must be positive")
	// ErrInvalidP 表示目标假阳性率 p 不合法（p <= 0 或 p >= 1）。
	ErrInvalidP = errors.New("bloom: target false positive rate p must be in the open interval (0, 1)")
)

// params 保存一个过滤器的构造参数。
//
// 推导公式（标准 Bloom filter 结论）：
//
//	m = ceil(-n * ln(p) / (ln(2)^2))   —— 位数组长度（比特）
//	k = ceil((m / n) * ln(2))           —— 哈希函数个数
//
// 在该 m、k 下，插入 n 个元素后任一比特仍为 0 的概率约为
// (1-1/m)^(kn) ≈ e^(-kn/m)，假阳性率约为
//
//	(1 - e^(-kn/m))^k ≈ p
type params struct {
	n int     // 目标元素数（仅用于构造）
	p float64 // 目标假阳性率
	m uint64  // 位数组长度（比特）
	k uint64  // 哈希函数个数
}

// deriveParams 由 (n, p) 推出 (m, k)。n<=0 或 p 不在 (0,1) 内返回可判定错误。
func deriveParams(n int, p float64) (params, error) {
	if n <= 0 {
		return params{}, ErrInvalidN
	}
	if math.IsNaN(p) || p <= 0 || p >= 1 {
		return params{}, ErrInvalidP
	}

	ln2 := math.Ln2
	mf := -float64(n) * math.Log(p) / (ln2 * ln2)
	m := uint64(math.Ceil(mf))
	if m < 1 {
		m = 1
	}
	k := uint64(math.Ceil((float64(m) / float64(n)) * ln2))
	if k < 1 {
		k = 1
	}
	return params{n: n, p: p, m: m, k: k}, nil
}

// M 返位数组长度（单位：比特）。
func (p params) M() uint64 { return p.m }

// K 返回哈希函数个数。
func (p params) K() uint64 { return p.k }

// words 返回容纳 m 个比特所需的 64 位字数量。
func (p params) words() uint64 {
	return (p.m + 63) >> 6
}
