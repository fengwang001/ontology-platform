// Package hash 只负责计数布隆过滤器的位置计算：
// 双重散列 h1/h2 与 k 个两两互异的位置，以及 m、k 的参数合法性校验。
// 本包不依赖项目内其他包。
package hash

import "errors"

// ErrInvalidParameters 是所有构造参数非法情况的唯一可判定哨兵错误：
// m 非素数、m <= k、k < 1。
var ErrInvalidParameters = errors.New("hash: invalid parameters")

// Params 保存一组已校验的散列参数。
type Params struct {
	m int
	k int
}

// New 校验并构造参数：m 必须是素数且 m > k，k >= 1。
func New(m, k int) (Params, error) {
	if k < 1 || m <= k || !isPrime(m) {
		return Params{}, ErrInvalidParameters
	}
	return Params{m: m, k: k}, nil
}

// M 返回计数器槽位总数。
func (p Params) M() int { return p.m }

// K 返回每个键访问的位置个数。
func (p Params) K() int { return p.k }

// H1 = x mod m。
func (p Params) H1(x int64) int { return int(x % int64(p.m)) }

// H2 = 1 + (x mod (m-1))，结果落在 1..m-1，与素数 m 互质。
func (p Params) H2(x int64) int { return 1 + int(x%int64(p.m-1)) }

// Positions 返回键 x 的 k 个位置 p_i = (h1 + i*h2) mod m，i=0..k-1。
// 每次返回新切片，多个 goroutine 可并发调用。
func (p Params) Positions(x int64) []int {
	base := int64(p.m)
	h1 := x % base
	h2 := 1 + x%(base-1)
	pos := make([]int, p.k)
	cur := h1
	for i := 0; i < p.k; i++ {
		pos[i] = int(cur)
		cur = (cur + h2) % base
	}
	return pos
}

// isPrime 对 m >= 2 做试除判定。
func isPrime(m int) bool {
	if m < 2 {
		return false
	}
	for d := 2; int64(d)*int64(d) <= int64(m); d++ {
		if m%d == 0 {
			return false
		}
	}
	return true
}
