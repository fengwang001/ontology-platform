// Package runs 把码点序列切成最长游程，并提供次数的十进制读写。
// 它不依赖本模块的其他包。
package runs

import (
	"errors"
	"math/big"
	"strconv"
)

// 次数规范形（≥2、无前导零的十进制）的拒绝原因，可用 errors.Is 判定。
var (
	ErrCountOne    = errors.New("runs: explicit count 1")
	ErrZeroCount   = errors.New("runs: zero count")
	ErrLeadingZero = errors.New("runs: leading zero in count")
	ErrBadDigit    = errors.New("runs: non-digit in count")
)

// Run 是一个最长游程：符号 Sym 连续出现 N 次。
type Run struct {
	Sym rune
	N   uint64
}

// Split 把 s 切成最长游程序列。不做任何 Unicode 规范化，
// 相同码点才合并，不同码点（如 é 与 e+U+0301）绝不合并。
func Split(s string) []Run {
	var out []Run
	for _, r := range s {
		if n := len(out); n > 0 && out[n-1].Sym == r {
			out[n-1].N++
		} else {
			out = append(out, Run{Sym: r, N: 1})
		}
	}
	return out
}

// AppendDecimal 把次数 n 以无符号、无前导零的十进制追加到 dst。
func AppendDecimal(dst []byte, n uint64) []byte {
	return strconv.AppendUint(dst, n, 10)
}

// ParseDecimal 把规范形十进制次数解析为任意精度整数，不会溢出。
// 次数必须 ≥2、无前导零、全是数字，否则返回对应的哨兵错误。
func ParseDecimal(digits string) (*big.Int, error) {
	switch {
	case digits == "" || digits == "0":
		return nil, ErrZeroCount
	case digits[0] == '0':
		return nil, ErrLeadingZero
	case digits == "1":
		return nil, ErrCountOne
	}
	for i := 0; i < len(digits); i++ {
		if digits[i] < '0' || digits[i] > '9' {
			return nil, ErrBadDigit
		}
	}
	n, _ := new(big.Int).SetString(digits, 10)
	return n, nil
}

// Exceeds 报告次数 n 是否大于上限 limit（纯整数比较，不分配）。
func Exceeds(n *big.Int, limit int64) bool {
	return n.Cmp(big.NewInt(limit)) > 0
}
