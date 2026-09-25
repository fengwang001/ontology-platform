// Package egcd 实现扩展欧几里得算法：求 gcd(a, b) 及贝祖系数 x、y。
package egcd

import (
	"errors"
	"math/bits"
)

// errDepth 是防御性哨兵：递归层数超过理论对数上界时返回（正常不会触发）。
var errDepth = errors.New("egcd: recursion depth exceeds logarithmic bound")

// ExtendedGCD 返回 g = gcd(a, b)（非负）及满足 a·x + b·y = g 的系数 x、y。
// 同输入返回确定的 (x, y) 对；为纯函数，可并发调用。
func ExtendedGCD(a, b int) (g, x, y int, err error) {
	sa, sb := sign(a), sign(b)
	ua, ub := abs(a), abs(b)
	g, x, y, depth := solve(ua, ub, 1)
	if depth > depthBound(ua, ub) {
		return 0, 0, 0, errDepth
	}
	return g, sa * x, sb * y, nil
}

// solve 在非负 (a, b) 上递归，depth 为非导出计数器「递归层数」。
// 回代关系（推导见 NOTES.md）：x = y'，y = x' − (a/b)·y'。
func solve(a, b, depth int) (g, x, y, d int) {
	if b == 0 {
		return a, 1, 0, depth
	}
	g, x1, y1, d := solve(b, a%b, depth+1)
	return g, y1, x1 - (a/b)*y1, d
}

// depthBound 返回层数上界 2·log2(max(a,b))+2，与欧几里得算法对数收敛一致。
func depthBound(a, b int) int {
	return 2*bits.Len64(uint64(max(a, b))) + 2
}

func sign(v int) int {
	switch {
	case v < 0:
		return -1
	case v > 0:
		return 1
	}
	return 0
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
