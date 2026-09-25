// Package hash 提供 Rabin-Karp 使用的固定窗口滚动哈希原语。
package hash

import "errors"

var (
	// ErrInvalidBase 表示 base 不大于 1。
	ErrInvalidBase = errors.New("hash: base must be > 1")
	// ErrInvalidMod 表示 mod 不大于 1。
	ErrInvalidMod = errors.New("hash: mod must be > 1")
	// ErrEmptyWindow 表示在空窗口上执行 Remove。
	ErrEmptyWindow = errors.New("hash: cannot remove from empty window")
)

// Rolling 是 h = (...((c0*base+c1)*base+c2)... ) % mod 形式的滚动哈希。
type Rolling struct {
	base uint64
	mod  uint64
	h    uint64
	len  int
}

// New 创建以 base、mod 为参数的滚动哈希，base>1 且 mod>1。
func New(base, mod uint64) (*Rolling, error) {
	if base <= 1 {
		return nil, ErrInvalidBase
	}
	if mod <= 1 {
		return nil, ErrInvalidMod
	}
	return &Rolling{base: base, mod: mod}, nil
}

// Append 把字符 c 追加到窗口末尾。
func (r *Rolling) Append(c byte) {
	r.h = (r.h*r.base + uint64(c)) % r.mod
	r.len++
}

// Slide 把长度固定的窗口向右滑动一位：移除开头字符 c 并追加字符 next。
// pow 为 base^(窗口长度-1) % mod。先加回 mod 再取模，避免减法产生负哈希。
func (r *Rolling) Slide(c, next byte, pow uint64) error {
	if r.len == 0 {
		return ErrEmptyWindow
	}
	term := uint64(c) % r.mod * pow % r.mod
	r.h = ((r.h+r.mod-term)%r.mod*r.base + uint64(next)) % r.mod
	return nil
}

// Value 返回当前窗口哈希。
func (r *Rolling) Value() uint64 { return r.h }

// Len 返回当前窗口字符数。
func (r *Rolling) Len() int { return r.len }

// Power 返回 base^n % mod，即长度 n+1 窗口最高位的权值。
func Power(base, mod, n uint64) (uint64, error) {
	if base <= 1 {
		return 0, ErrInvalidBase
	}
	if mod <= 1 {
		return 0, ErrInvalidMod
	}
	pow := uint64(1) % mod
	for i := uint64(0); i < n; i++ {
		pow = pow * base % mod
	}
	return pow, nil
}
