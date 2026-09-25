// Package check 提供朴素参照实现，用于对照滑动窗口结果。
package check

// Naive 枚举所有子串逐一查重，返回最长无重复字符子串长度。O(n^2)。
func Naive(s string) int {
	best := 0
	for i := 0; i < len(s); i++ {
		seen := make(map[byte]bool, 8)
		for j := i; j < len(s); j++ {
			if seen[s[j]] {
				break
			}
			seen[s[j]] = true
			if j-i+1 > best {
				best = j - i + 1
			}
		}
	}
	return best
}
