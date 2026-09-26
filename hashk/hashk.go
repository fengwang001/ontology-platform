// Package hashk 提供布谷鸟哈希的两个候选槽位计算函数。
// 不依赖任何其他包。
package hashk

// H1 返回键 x 在 T1 中的候选槽：x mod n。
func H1(x, n int) int {
	return mod(x, n)
}

// H2 返回键 x 在 T2 中的候选槽：(x*3 + 1) mod n。
func H2(x, n int) int {
	return mod(x*3+1, n)
}

// mod 对负数键也返回 [0, n) 内的非负余数。
func mod(x, n int) int {
	r := x % n
	if r < 0 {
		r += n
	}
	return r
}
