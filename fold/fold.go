// Package fold 提供逐 rune 的大小写折叠，把字符串规范成折叠键。
package fold

import (
	"strings"
	"sync/atomic"
	"unicode"
)

// lastRunes 记录最近一次 String 处理的 rune 数（非导出，不进公开接口）。
var lastRunes atomic.Int64

// Rune 把 r 折叠到其 unicode.SimpleFold 轨道上码点最小的 rune。
// 轨道最小值是轨道上的不动点，因此折叠幂等。
func Rune(r rune) rune {
	min := r
	for f := unicode.SimpleFold(r); f != r; f = unicode.SimpleFold(f) {
		if f < min {
			min = f
		}
	}
	return min
}

// String 逐 rune 折叠 s 得到规范键；单遍扫描，rune 数守恒。
func String(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	n := int64(0)
	for _, r := range s {
		b.WriteRune(Rune(r))
		n++
	}
	lastRunes.Store(n)
	return b.String()
}
