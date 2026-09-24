// Package runs 把码点序列切成最长游程，并提供游程次数的十进制读写。
// 次数用 math/big.Int 表示，因此不会因次数超过 int64 而溢出。
package runs

import (
	"errors"
	"math/big"
	"unicode/utf8"
)

// Run 表示符号 Symbol 连续重复 Count 次（Count 恒为正）。
type Run struct {
	Symbol rune
	Count  *big.Int
}

// Split 将 s 切成若干个最长游程（按码点比较，不做任何规范化）。
func Split(s string) []Run {
	var out []Run
	for len(s) > 0 {
		r, size := utf8.DecodeRuneInString(s)
		count := big.NewInt(1)
		s = s[size:]
		for len(s) > 0 {
			next, n := utf8.DecodeRuneInString(s)
			if next != r {
				break
			}
			count.Add(count, big.NewInt(1))
			s = s[n:]
		}
		out = append(out, Run{Symbol: r, Count: count})
	}
	return out
}

// AppendCount 把正整数 n 的无前导零十进制追加到 dst。
func AppendCount(dst []byte, n *big.Int) []byte {
	return n.Append(dst, 10)
}

// ErrEmptyCount 表示十进制次数为空。
var ErrEmptyCount = errors.New("runs: empty count")

// ErrCountDigit 表示次数中出现非 ASCII 数字字节，Offset 为其位置。
type ErrCountDigit struct {
	Offset int
}

func (e *ErrCountDigit) Error() string { return "runs: non-digit byte in count" }

// ParseCount 解析 p 中的十进制数字（无符号）为 *big.Int，返回消费的字节数。
// 空串返回 ErrEmptyCount；出现非数字返回 ErrCountDigit，不做任何溢出截断。
func ParseCount(p []byte) (*big.Int, int, error) {
	n := 0
	for n < len(p) && p[n] >= '0' && p[n] <= '9' {
		n++
	}
	if n == 0 {
		return nil, 0, ErrEmptyCount
	}
	if n < len(p) {
		return nil, 0, &ErrCountDigit{Offset: n}
	}
	v, ok := new(big.Int).SetString(string(p[:n]), 10)
	if !ok {
		return nil, 0, &ErrCountDigit{Offset: 0}
	}
	return v, n, nil
}
