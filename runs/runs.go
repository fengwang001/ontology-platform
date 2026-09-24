// Package runs 把码点序列切成最长游程，并提供游程次数的十进制读写。
// 它不依赖工程内的其他包。
package runs

import (
	"errors"
	"math"
	"strconv"
)

// MaxCount 是单个游程允许的最大次数。解码输出的长度不可能超过 math.MaxInt
//（字节串长度受 int 表示与地址空间限制），超过它的次数在本平台无意义。
const MaxCount = math.MaxInt

// ErrCountOverflow 表示次数大于 MaxCount 或十进制解析溢出；解析过程绝不静默回绕。
var ErrCountOverflow = errors.New("runs: run count overflow")

// Run 是一个最长游程：码点 R 连续出现 N 次。
type Run struct {
	R rune
	N int
}

// Split 把码点序列切成最长游程。调用方需保证 s 为合法 UTF-8。
func Split(s string) []Run {
	var out []Run
	for _, r := range s {
		if n := len(out); n > 0 && out[n-1].R == r {
			out[n-1].N++
			continue
		}
		out = append(out, Run{R: r, N: 1})
	}
	return out
}

// AppendCount 把次数 n（n >= 1）的无前导零十进制表示追加到 dst。
func AppendCount(dst []byte, n int) []byte {
	return strconv.AppendInt(dst, int64(n), 10)
}

// AddCountDigit 在已有次数 n 后追加一个十进制位 d（0..9）。
// 结果超过 MaxCount 时返回 ErrCountOverflow，不产生静默溢出。
func AddCountDigit(n int, d byte) (int, error) {
	v := int(d) - '0'
	if n > (MaxCount-v)/10 {
		return 0, ErrCountOverflow
	}
	return n*10 + v, nil
}
