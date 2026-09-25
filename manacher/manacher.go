// Package manacher 以 Manacher 算法在线性时间计算奇/偶回文半径 d1、d2。
// 不依赖其他包。
package manacher

// cmpCount 非导出计数器：计算 d1/d2 期间逐字符比较的总次数。
// 只在包内（白盒测试）可读，不出现在任何公开接口里。
var cmpCount int

// Compute 返回 s（按码点）的 d1、d2 半径数组。
// d1[i] = 以 s[i] 为中心的奇回文个数（含半径 0）；d2 长度 n+1，
// d2[i] = 以 s[i-1]|s[i] 之间为中心的偶回文个数，d2[0] 恒 0。
func Compute(s []rune) (d1, d2 []int) {
	n := len(s)
	d1 = make([]int, n)
	d2 = make([]int, n+1)
	cmpCount = 0

	l, r := 0, -1 // 当前最右奇回文盒子 [l, r]
	for i := 0; i < n; i++ {
		k := 1
		if i <= r {
			k = min(d1[l+r-i], r-i+1)
		}
		for i-k >= 0 && i+k < n && eq(s[i-k], s[i+k]) {
			k++
		}
		d1[i] = k
		if i+k-1 > r {
			l, r = i-k+1, i+k-1
		}
	}

	l, r = 0, -1 // 当前最右偶回文盒子 [l, r]
	for i := 1; i <= n; i++ {
		k := 0
		if i <= r {
			k = min(d2[l+r-i+1], r-i+1)
		}
		for i-k-1 >= 0 && i+k < n && eq(s[i-k-1], s[i+k]) {
			k++
		}
		d2[i] = k
		if i+k-1 > r {
			l, r = i-k, i+k-1
		}
	}
	return d1, d2
}

// eq 比较两个码点并计入总比较次数。
func eq(a, b rune) bool {
	cmpCount++
	return a == b
}
