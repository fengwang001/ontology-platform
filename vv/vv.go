// Package vv 提供版本向量的字典序比较、逐分量 min/max 与合法性判定。
// 它不依赖项目内任何其他包。
package vv

import "slices"

// Vector 是长度等于副本数 R 的版本向量，各分量为非负整数。
type Vector []int

// Cmp 按分量顺序字典序比较 a 与 b：-1 表示 a<b，0 表示相等，1 表示 a>b。
// 前缀相等时较短者为小（标准字典序）；本项目中合法向量恒等长。
func Cmp(a, b Vector) int { return slices.Compare(a, b) }

// Less 报告 a 是否字典序严格小于 b。
func Less(a, b Vector) bool { return Cmp(a, b) < 0 }

// MergeMax 返回逐分量 max(dst[i], src[i]) 的新向量，不修改入参。
func MergeMax(dst, src Vector) Vector {
	out := append(Vector(nil), dst...)
	for i, x := range src {
		if i >= len(out) {
			out = append(out, x)
			continue
		}
		if x > out[i] {
			out[i] = x
		}
	}
	return out
}

// MinPointwise 返回逐分量最小值组成的新向量。入参必须非空且等长。
func MinPointwise(vs []Vector) Vector {
	out := append(Vector(nil), vs[0]...)
	for _, v := range vs[1:] {
		for i := range out {
			if v[i] < out[i] {
				out[i] = v[i]
			}
		}
	}
	return out
}

// LeqPointwise 报告 a 是否逐分量 a[i] <= b[i]。
func LeqPointwise(a, b Vector) bool {
	for i, x := range a {
		if x > b[i] {
			return false
		}
	}
	return true
}

// Valid 报告向量是否合法：长度恰为 R 且不含负分量（R 由调用方保证为正）。
func Valid(v Vector, r int) bool {
	if len(v) != r {
		return false
	}
	for _, x := range v {
		if x < 0 {
			return false
		}
	}
	return true
}
