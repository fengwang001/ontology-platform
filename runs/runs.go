// Package runs 把码点序列切成最长游程，并提供不溢出的十进制次数读写。
// 它不依赖本模块的其他包。
package runs

import (
	"errors"
	"math"
	"unicode/utf8"
)

// Run 是一个「符号重复 Count 次」的游程；Symbol 为 utf8.RuneError
// 表示一段无法解码为合法 Unicode 码点的输入字节。
type Run struct {
	Symbol rune
	Count  int
	// Invalid 为 true 时，Symbol 无意义，BadBytes 是这段非法字节的长度。
	Invalid  bool
	BadBytes int
}

// MaxCount 是允许的最大游程次数。规范编码来自 Go 字符串，游程长度
// 不可能超过 MaxInt，因此更大的次数不可能是规范形式。
const MaxCount = math.MaxInt

// ErrCountTooLarge 表示十进制次数超过 MaxCount。
var ErrCountTooLarge = errors.New("runs: count exceeds MaxInt")

// Split 将 s 切成最长游程（连续相同码点合并）。不做任何 Unicode
// 规范化；非法 UTF-8 字节各自成为 Invalid 游程。
func Split(s string) []Run {
	var out []Run
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			out = append(out, Run{Invalid: true, BadBytes: 1, Count: 1})
			i++
			continue
		}
		n := 1
		for i+size < len(s) {
			next, nextSize := utf8.DecodeRuneInString(s[i+size:])
			if next == utf8.RuneError && nextSize == 1 {
				break
			}
			if next != r {
				break
			}
			size += nextSize
			n++
		}
		out = append(out, Run{Symbol: r, Count: n})
		i += size
	}
	return out
}

// AppendCount 把 n（必须 >= 0）的无前导零十进制表示追加到 b。
func AppendCount(b []byte, n int) []byte {
	if n == 0 {
		return append(b, '0')
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return append(b, buf[i:]...)
}

// ParseCount 从 s 开头解析十进制次数。digitLen 为 0 表示次数被省略，
// 此时返回 1。次数超过 MaxCount 时返回 ErrCountTooLarge，绝不溢出。
func ParseCount(s string) (count, digitLen int, err error) {
	n := 0
	for digitLen < len(s) && s[digitLen] >= '0' && s[digitLen] <= '9' {
		d := int(s[digitLen] - '0')
		if n > (MaxCount-d)/10 {
			return 0, digitLen, ErrCountTooLarge
		}
		n = n*10 + d
		digitLen++
	}
	if digitLen == 0 {
		return 1, 0, nil
	}
	return n, digitLen, nil
}
