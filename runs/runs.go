// Package runs 把码点序列切成最长游程，并提供不溢出的次数十进制读写。
package runs

import (
	"errors"
	"math/big"
	"strconv"
	"unicode/utf8"
)

// ErrNotDigits 表示待解析的次数串为空或含非 ASCII 数字。
var ErrNotDigits = errors.New("runs: not a decimal count")

// Run 是一个最长游程：同一个码点连续出现 Count 次。
type Run struct {
	Rune  rune
	Count int64
}

// Split 把 s 切成最长游程序列。输入须为合法 UTF-8。
func Split(s string) []Run {
	var out []Run
	for _, r := range s {
		if n := len(out); n > 0 && out[n-1].Rune == r {
			out[n-1].Count++
		} else {
			out = append(out, Run{Rune: r, Count: 1})
		}
	}
	return out
}

// ParseCount 把十进制数字串解析为任意精度整数，不会溢出。
// digits 必须非空且只含 ASCII 数字，否则返回 ErrNotDigits。
func ParseCount(digits string) (*big.Int, error) {
	if digits == "" {
		return nil, ErrNotDigits
	}
	for i := 0; i < len(digits); i++ {
		if digits[i] < '0' || digits[i] > '9' {
			return nil, ErrNotDigits
		}
	}
	n, ok := new(big.Int).SetString(digits, 10)
	if !ok {
		return nil, ErrNotDigits
	}
	return n, nil
}

// CountString 返回次数 n 的十进制写法（无符号、无前导零）。
func CountString(n int64) string {
	return strconv.FormatInt(n, 10)
}

// AppendRun 把 r 重复 n 次追加到 dst。若追加后总长度会超过 limit，
// 则不追加并返回 ok=false；调用方据此拒绝，而不是按次数分配内存。
func AppendRun(dst []byte, r rune, n *big.Int, limit int64) (out []byte, ok bool) {
	need := new(big.Int).Mul(n, big.NewInt(int64(utf8.RuneLen(r))))
	if !need.IsInt64() || need.Int64() > limit-int64(len(dst)) {
		return dst, false
	}
	var tmp [utf8.UTFMax]byte
	w := utf8.EncodeRune(tmp[:], r)
	for i, m := int64(0), need.Int64(); i < m; i++ {
		dst = append(dst, tmp[:w]...)
	}
	return dst, true
}
