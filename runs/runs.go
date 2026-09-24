// Package runs 把码点序列切成最长游程，并提供不溢出的十进制次数读写。
package runs

import (
	"errors"
	"math"
	"strconv"
)

// ErrOverflow 表示十进制次数超出 uint64 可表示的范围。
var ErrOverflow = errors.New("runs: decimal count overflows uint64")

// Each 按码点遍历 s，把连续相同码点合成最长游程，逐游程调用 fn。
// 不做任何 Unicode 规范化：é 与 e+U+0301 是不同的码点序列。
func Each(s string, fn func(r rune, n uint64)) {
	var prev rune
	var n uint64
	started := false
	for _, r := range s {
		if started && r == prev {
			n++
			continue
		}
		if started {
			fn(prev, n)
		}
		prev, n, started = r, 1, true
	}
	if started {
		fn(prev, n)
	}
}

// AddDigit 把一位十进制数字 d（0–9）并入累计值 n；结果溢出 uint64 时
// 返回 ErrOverflow，调用方必须在首次溢出处停止，不得回绕续用。
func AddDigit(n, d uint64) (uint64, error) {
	if n > (math.MaxUint64-d)/10 {
		return 0, ErrOverflow
	}
	return n*10 + d, nil
}

// AppendDecimal 把 n 以无符号、无前导零的十进制追加到 b。
func AppendDecimal(b []byte, n uint64) []byte {
	return strconv.AppendUint(b, n, 10)
}
