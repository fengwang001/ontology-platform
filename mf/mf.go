// Package mf 提供单次分配计算：精确有理数、公平份额、全额满足判定。
package mf

import "strconv"

// Frac 是已约分的精确分数 N/D，恒有 D > 0。
type Frac struct {
	N int64
	D int64
}

func gcd(a, b int64) int64 {
	if a < 0 {
		a = -a
	}
	if b < 0 {
		b = -b
	}
	for b != 0 {
		a, b = b, a%b
	}
	if a == 0 {
		return 1
	}
	return a
}

// New 构造已约分、分母为正的分数。d 为 0 时视为 0/1（本包内不会触发）。
func New(n, d int64) Frac {
	if d == 0 {
		return Frac{0, 1}
	}
	if d < 0 {
		n, d = -n, -d
	}
	g := gcd(n, d)
	return Frac{n / g, d / g}
}

// Fair 返回「剩余容量 / 剩余任务数」的精确公平份额。count 必须为正。
func Fair(remaining, count int64) Frac {
	return New(remaining, count)
}

// Leq 判定整数 d 是否不超过分数 f（交叉相乘，精确）。
func Leq(d int64, f Frac) bool {
	return d*f.D <= f.N
}

// Cmp 比较两个分数：a<b 返回 -1，相等 0，a>b 返回 1（交叉相乘，精确）。
func Cmp(a, b Frac) int {
	l, r := a.N*b.D, b.N*a.D
	switch {
	case l < r:
		return -1
	case l > r:
		return 1
	default:
		return 0
	}
}

// Add 返回两分数之和（精确约分）。
func Add(a, b Frac) Frac {
	return New(a.N*b.D+b.N*a.D, a.D*b.D)
}

// MulInt 返回 f 乘整数 k。
func MulInt(f Frac, k int64) Frac {
	return New(f.N*k, f.D)
}

// IsZero 报告 f 是否为 0。
func IsZero(f Frac) bool {
	return f.N == 0
}

func (f Frac) String() string {
	if f.D == 1 {
		return strconv.FormatInt(f.N, 10)
	}
	return strconv.FormatInt(f.N, 10) + "/" + strconv.FormatInt(f.D, 10)
}
