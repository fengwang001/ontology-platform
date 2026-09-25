// Package num 提供整数类型与模反元素，错误用哨兵值暴露。
package num

import (
	"errors"

	"ontology/egcd"
)

// Int 是包内统一的整数类型（64 位平台上即 int64 宽度，大数不溢出）。
type Int = int

// 哨兵错误，调用方用 errors.Is 区分。
var (
	// ErrBadArg 表示非法输入：m <= 0 或 a < 0。
	ErrBadArg = errors.New("num: bad argument")
	// ErrNoInverse 表示逆元不存在：gcd(a, m) != 1。
	ErrNoInverse = errors.New("num: no modular inverse")
)

// ModInverse 返回 a 模 m 的逆元 r（0 <= r < m），满足 a·r ≡ 1 (mod m)。
// gcd(a, m) == 1 时贝祖系数 x 即逆元（可能为负，规范化到 [0, m)）。
func ModInverse(a, m Int) (Int, error) {
	if m <= 0 || a < 0 {
		return 0, ErrBadArg
	}
	g, x, _, err := egcd.ExtendedGCD(a, m)
	if err != nil {
		return 0, err
	}
	if g != 1 {
		return 0, ErrNoInverse
	}
	return (x%m + m) % m, nil
}
