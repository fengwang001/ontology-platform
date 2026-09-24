// Package runs 把码点序列切成最长游程，并提供游程次数的十进制读写。
// 本包不依赖工程内其他包。
package runs

import "strconv"

// Run 是一个最长游程：Symbol 连续重复 Count 次。
type Run struct {
	Symbol rune
	Count  uint64
}

// Split 把 s 切成最长游程序列；空串返回 nil。
// 不做任何 Unicode 规范化：é 与 e+U+0301 是不同的码点序列。
func Split(s string) []Run {
	var rs []Run
	for _, r := range s {
		if n := len(rs); n > 0 && rs[n-1].Symbol == r {
			rs[n-1].Count++
		} else {
			rs = append(rs, Run{Symbol: r, Count: 1})
		}
	}
	return rs
}

// AppendCount 把 n 的十进制表示（无符号、无前导零）追加到 dst。
func AppendCount(dst []byte, n uint64) []byte {
	return strconv.AppendUint(dst, n, 10)
}

// ParseCount 解析非空 ASCII 数字串 digits 为十进制数。
// 若真值超过 max，则返回 max+1（饱和），数学上不会溢出；
// 调用方据此即可判定“超过预算”，无需支持任意精度整数。
// 要求 max < math.MaxUint64。
func ParseCount(digits string, max uint64) uint64 {
	var n uint64
	for i := 0; i < len(digits); i++ {
		d := uint64(digits[i] - '0')
		if n > max/10 || (n == max/10 && d > max%10) {
			return max + 1
		}
		n = n*10 + d
	}
	return n
}
