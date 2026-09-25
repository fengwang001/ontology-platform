// Package sar 实现固定位宽序号的回绕算术：模 M 差与四态相对比较。
// 不依赖任何其他包。
package sar

// Rel 是四态相对比较结果。
type Rel int

const (
	Equal        Rel = iota // d == 0
	Less                    // 0 < d < M/2：a 在 b 之前
	Greater                 // d > M/2：a 在 b 之后
	Incomparable            // d == M/2：半圈临界，无法判定
)

func (r Rel) String() string {
	switch r {
	case Equal:
		return "Equal"
	case Less:
		return "Less"
	case Greater:
		return "Greater"
	default:
		return "Incomparable"
	}
}

// Mod 返回位宽 n 对应的模 M = 1<<n。调用方保证 1 <= n <= 63。
func Mod(n int) uint64 { return uint64(1) << n }

// Diff 返回模 mod 的无符号差 d = (b - a) & (mod-1)，自然回绕。
func Diff(a, b, mod uint64) uint64 { return (b - a) & (mod - 1) }

// Cmp 比较 a 与 b 在模 mod 序号环上的相对位置。
// d == 0 → Equal；0 < d < M/2 → Less；d == M/2 → Incomparable；d > M/2 → Greater。
func Cmp(a, b, mod uint64) Rel {
	d := Diff(a, b, mod)
	half := mod >> 1
	switch {
	case d == 0:
		return Equal
	case d < half:
		return Less
	case d == half:
		return Incomparable
	default:
		return Greater
	}
}
