// Package win 提供最长无重复字符子串的滑动窗口实现。
package win

import "sync/atomic"

// visits 是非导出计数器，统计字符访问次数（右边界读字符 + 左边界查表），
// 用于测试断言单趟线性上界 2n。原子操作保证并发调用 -race 干净。
var visits atomic.Int64

// Visits 返回累计字符访问次数（仅供测试断言复杂度上界）。
func Visits() int64 { return visits.Load() }

// ResetVisits 清零字符访问计数器（仅供测试隔离用例）。
func ResetVisits() { visits.Store(0) }

// LongestSubstring 返回 s 中最长无重复字符子串的长度。空串返回 0。
func LongestSubstring(s string) int {
	lo, hi := LongestSubstringRange(s)
	return hi - lo
}

// LongestSubstringRange 返回 s 中最长无重复字符子串的区间 [lo, hi)。
// 多解时任取其一（首个最长者），与 LongestSubstring 自洽。
func LongestSubstringRange(s string) (lo, hi int) {
	var last [256]int // 各字节上一次出现的位置，-1 表示未出现
	for i := range last {
		last[i] = -1
	}
	start, bestLo, bestHi := 0, 0, 0
	for end := 0; end < len(s); end++ {
		visits.Add(1) // 右边界访问：每个字符恰一次
		c := s[end]
		visits.Add(1) // 左边界访问：查表判断收缩，每个字符至多一次
		if last[c] >= start {
			// 直接跳到重复字符上一次出现位置的下一个位置，
			// 而不是每次只 +1（见 NOTES.md 推导）。
			start = last[c] + 1
		}
		last[c] = end
		if end+1-start > bestHi-bestLo {
			bestLo, bestHi = start, end+1
		}
	}
	return bestLo, bestHi
}
