// Package hash 提供 Rabin-Karp 使用的固定参数滚动哈希。
package hash

import "errors"

// ErrInvalidParam 表示滚动哈希参数非法（base<2 或 mod<2）。
var ErrInvalidParam = errors.New("hash: invalid base or mod")

const (
	// Base 是固定小基数，Mod 是固定小模数，便于构造可复现碰撞。
	Base = 10
	Mod  = 7
)

// Roller 保存当前窗口的滚动哈希值，零值即可用。
type Roller struct {
	base uint64
	mod  uint64
	pow  uint64 // base^(m-1) % mod，窗口最高位权值
	h    uint64
}

// New 以给定参数创建滚动器，参数非法返回 ErrInvalidParam。
func New(base, mod uint64) (*Roller, error) {
	if base < 2 || mod < 2 {
		return nil, ErrInvalidParam
	}
	return &Roller{base: base, mod: mod, pow: 1}, nil
}

// Append 把一个字节追加到窗口尾部：h = (h*base + code(c)) % mod。
func (r *Roller) Append(c byte) {
	r.h = (r.h*r.base + code(c)) % r.mod
}

// SetLength 按窗口长度 m 计算最高位权值 base^(m-1)%mod。
func (r *Roller) SetLength(m int) {
	r.pow = 1
	for i := 0; i < m-1; i++ {
		r.pow = r.pow * r.base % r.mod
	}
}

// Shift 滑窗一步：移出最高位 out、移入最低位 in。
// 先加 mod 再取模，避免减法产生负数哈希。
func (r *Roller) Shift(out, in byte) {
	r.h = (r.h - code(out)*r.pow%r.mod + r.mod) % r.mod
	r.h = (r.h*r.base + code(in)) % r.mod
}

// Value 返回当前哈希值，范围 [0, mod)。
func (r *Roller) Value() uint64 { return r.h }

// code 是字母表数值；非小写字母按 uint8 回绕不影响同余性质，
// 正确性由命中后的逐字符验证保证。
func code(c byte) uint64 { return uint64(c - 'a' + 1) }
