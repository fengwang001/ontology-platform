// Package fold 提供逐 rune 的大小写折叠，把字符串规范化为折叠键。
package fold

import (
	"strings"
	"sync/atomic"
	"unicode"
)

// lastRunes 记录最近一次折叠处理的 rune 数（非导出，仅内部观测）。
var lastRunes atomic.Int64

// Rune 返回 r 的折叠形式：SimpleFold 轨道内的小写不动点（取最小者），
// 轨道内没有小写不动点时退化为轨道最小 rune；幂等由构造保证。
func Rune(r rune) rune {
	min, lower := r, r
	hasLower := unicode.ToLower(r) == r
	for s := unicode.SimpleFold(r); s != r; s = unicode.SimpleFold(s) {
		if s < min {
			min = s
		}
		if unicode.ToLower(s) == s && (!hasLower || s < lower) {
			lower, hasLower = s, true
		}
	}
	if hasLower {
		return lower
	}
	return min
}

// Fold 返回 s 的折叠形式（规范键），长度（rune 数）与输入一致。
func Fold(s string) string {
	f, _ := foldN(s)
	return f
}

// FoldCount 返回 s 的折叠形式与实际处理的 rune 数。
func FoldCount(s string) (string, int) {
	return foldN(s)
}

func foldN(s string) (string, int) {
	var b strings.Builder
	b.Grow(len(s))
	n := 0
	for _, r := range s {
		b.WriteRune(Rune(r))
		n++
	}
	lastRunes.Store(int64(n))
	return b.String(), n
}
