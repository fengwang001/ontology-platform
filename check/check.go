// Package check 提供朴素子串匹配参照实现，用于对照验证 Rabin-Karp。
package check

// Naive 逐位置比较，返回 pattern 在 text 中所有出现位置（升序，含重叠）。
func Naive(text, pattern string) []int {
	m := len(pattern)
	if m == 0 || m > len(text) {
		return nil
	}
	var out []int
	for i := 0; i+m <= len(text); i++ {
		if text[i:i+m] == pattern {
			out = append(out, i)
		}
	}
	return out
}
