// Package win 用滑动窗口求最长无重复字符子串。
package win

import "sync/atomic"

var visits atomic.Int64 // 字符访问次数（非导出计数器，用于线性度断言）

// Visits 返回累计字符访问次数。
func Visits() int64 { return visits.Load() }

// ResetVisits 清零字符访问计数器。
func ResetVisits() { visits.Store(0) }

// LongestSubstring 返回 s 中最长无重复字符子串的长度。
func LongestSubstring(s string) int {
	lo, hi := LongestSubstringRange(s)
	return hi - lo
}

// LongestSubstringRange 返回最长无重复字符子串之一的区间 [lo, hi)。
// 遇重复时左边界直接跳到重复字符上一次出现位置 + 1。
func LongestSubstringRange(s string) (lo, hi int) {
	last := make(map[byte]int, 16)
	start, bestLo, bestHi := 0, 0, 0
	for r := 0; r < len(s); r++ {
		visits.Add(1) // 右边界访问 s[r]，每字符恰一次
		c := s[r]
		if p, ok := last[c]; ok && p >= start {
			start = p + 1 // 跳跃式收缩，不做字符访问
		}
		last[c] = r
		if r+1-start > bestHi-bestLo {
			bestLo, bestHi = start, r+1
		}
	}
	return bestLo, bestHi
}
