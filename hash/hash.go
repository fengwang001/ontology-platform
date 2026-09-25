// Package hash 提供多项式滚动哈希：h = (h*base + c) % mod。
package hash

// Rolling 维护固定长度窗口的滚动哈希值。
type Rolling struct {
	base uint64
	mod  uint64
	pow  uint64 // base^(window-1) % mod
	h    uint64
	n    int
}

// New 创建窗口长度为 window 的滚动哈希。window 必须 >= 1。
func New(base, mod uint64, window int) *Rolling {
	r := &Rolling{base: base, mod: mod, pow: 1}
	for i := 0; i < window-1; i++ {
		r.pow = r.pow * base % mod
	}
	return r
}

// Append 在窗口右端加入字节 c。
func (r *Rolling) Append(c byte) {
	r.h = (r.h*r.base + uint64(c)) % r.mod
	r.n++
}

// Remove 从窗口左端删去最高位字节 c。
// h - c*pow 可能为负，先加 mod 再取模，保证结果落在 [0, mod)。
func (r *Rolling) Remove(c byte) {
	sub := uint64(c) % r.mod * r.pow % r.mod
	r.h = (r.h + r.mod - sub) % r.mod
	r.n--
}

// Value 返回当前窗口的哈希值。
func (r *Rolling) Value() uint64 { return r.h }

// Len 返回当前窗口内字节数。
func (r *Rolling) Len() int { return r.n }
