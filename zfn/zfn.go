// Package zfn 用 Z-box 优化在线性时间内计算字符串（码点序列）的 Z 数组。
package zfn

import "errors"

// ErrBudgetExceeded 表示逐字符比较次数超过 2n 预算。
// 错误中不携带计数器的数值，计数器本身永不离开本包。
var ErrBudgetExceeded = errors.New("zfn: linear comparison budget exceeded")

// Z 持有一次 Z 数组计算的结果。所有字段均为非导出。
type Z struct {
	rs []rune
	z  []int
	// comparisons 记录计算过程中逐字符比较的总次数（含失配那次）。
	// 它只能被同包白盒测试直接读取，不出现在任何公开接口中。
	comparisons int
}

// New 对码点序列 rs 计算 Z 数组。
// 约定 Z[0]=0；Z-box 内的位置只拷贝初值，不逐对重扫。
func New(rs []rune) *Z {
	n := len(rs)
	x := &Z{rs: rs, z: make([]int, n)}
	// [l,r) 为当前最右 Z-box；r 在整个算法中单调不减。
	l, r := 0, 0
	for i := 1; i < n; i++ {
		if i < r {
			x.z[i] = min(r-i, x.z[i-l])
		}
		for i+x.z[i] < n {
			// 每次真正的逐字符比较都计数：成功则推进，失败即停。
			x.comparisons++
			if rs[x.z[i]] != rs[i+x.z[i]] {
				break
			}
			x.z[i]++
		}
		if i+x.z[i] > r {
			l, r = i, i+x.z[i]
		}
	}
	return x
}

// Len 返回码点长度 n。
func (x *Z) Len() int { return len(x.z) }

// At 返回 Z[i]；越界返回 0。
func (x *Z) At(i int) int {
	if i < 0 || i >= len(x.z) {
		return 0
	}
	return x.z[i]
}

// Array 返回 Z 数组的副本，调用方修改不影响内部状态。
func (x *Z) Array() []int {
	out := make([]int, len(x.z))
	copy(out, x.z)
	return out
}

// CheckLinear 判定本次计算的逐字符比较总次数是否不超过 2n。
// 它只返回成败，不泄露计数器的具体数值。
func (x *Z) CheckLinear() error {
	if x.comparisons > 2*len(x.rs) {
		return ErrBudgetExceeded
	}
	return nil
}
