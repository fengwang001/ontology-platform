// Package check 提供朴素参照实现，用于对照验证 egcd/num。
package check

import "math/big"

// NaiveGCD 枚举所有约数求 gcd（非负），仅适合小数值对照。
func NaiveGCD(a, b int) int {
	a, b = abs(a), abs(b)
	g := 0
	for d := 1; d <= max(a, b); d++ {
		if a%d == 0 && b%d == 0 {
			g = d
		}
	}
	return g
}

// RefGCD 用 math/big 求 gcd 与贝祖系数，作为权威对照。
func RefGCD(a, b int) (g, x, y int) {
	bx, by, bg := new(big.Int), new(big.Int), new(big.Int)
	bg.GCD(bx, by, big.NewInt(int64(a)), big.NewInt(int64(b)))
	return int(bg.Int64()), int(bx.Int64()), int(by.Int64())
}

// RefDepth 统计扩展欧几里得在非负输入上的递归层数。
func RefDepth(a, b int) int {
	if b == 0 {
		return 1
	}
	return RefDepth(b, a%b) + 1
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
