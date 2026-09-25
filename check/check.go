// Package check 提供朴素参照与错误对照实现，供测试比对 win 包。
package check

// Naive 枚举所有子串逐一查重，返回最长无重复字符子串长度。
func Naive(s string) int {
	best := 0
	for i := 0; i < len(s); i++ {
		var seen [256]bool
		for j := i; j < len(s) && !seen[s[j]]; j++ {
			seen[s[j]] = true
			if j-i+1 > best {
				best = j - i + 1
			}
		}
	}
	return best
}

// Distinct 报告 s 是否不含重复字符。
func Distinct(s string) bool {
	var seen [256]bool
	for i := 0; i < len(s); i++ {
		if seen[s[i]] {
			return false
		}
		seen[s[i]] = true
	}
	return true
}

// buggy 是「每次只 +1」的错误收缩实现（第三节反例，内联作测试对照）：
// 左边界跳转不及时，在 "abba" 上返回 3 而非正确的 2。
func buggy(s string) int {
	var last [256]int // 存位置+1，0 表示未出现
	lo, best := 0, 0
	for hi := 0; hi < len(s); hi++ {
		if last[s[hi]] > lo {
			lo++ // 错误：只 +1，不跳到重复字符的下一个位置
		}
		last[s[hi]] = hi + 1
		if hi-lo+1 > best {
			best = hi - lo + 1
		}
	}
	return best
}
