// Package runs 把码点序列切成最长游程，并提供不溢出的十进制次数读写。
package runs

import (
	"math"
	"strconv"
)

// MaxCount 是单个游程允许的最大次数：切片/字符串长度的天然上限。
const MaxCount = math.MaxInt

// Run 表示 Symbol 连续出现 Count 次。
type Run struct {
	Symbol rune
	Count  int
}

// Split 把 s 切成最长游程（相邻相同码点合并）。不做 Unicode 规范化。
func Split(s string) []Run {
	var out []Run
	for _, r := range s {
		if n := len(out); n > 0 && out[n-1].Symbol == r {
			out[n-1].Count++
		} else {
			out = append(out, Run{Symbol: r, Count: 1})
		}
	}
	return out
}

// AppendCount 以无前导零的十进制形式追加次数；次数必须 >= 1。
func AppendCount(b []byte, count int) []byte {
	return strconv.AppendInt(b, int64(count), 10)
}

// AppendDigit 把十进制位 d(0..9) 累加到 n；超出 MaxCount 时返回 ok=false，
// 且不回绕、不修改 n。
func AppendDigit(n int, d byte) (int, bool) {
	v := int(d - '0')
	if n > (MaxCount-v)/10 {
		return n, false
	}
	return n*10 + v, true
}

// IsCountDigit 报告 b 是否为 ASCII 数字。
func IsCountDigit(b byte) bool { return b >= '0' && b <= '9' }
