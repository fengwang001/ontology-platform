// Package runs 把码点序列切成最长游程，并提供次数的十进制读写。
// 本包不依赖其他包。
package runs

import (
	"math"
	"strconv"
)

// Run 表示连续相同码点的一个最长游程。
type Run struct {
	Sym rune
	N   int
}

// MaxCount 是允许的最大游程次数。
const MaxCount = math.MaxInt

// SplitRunes 将码点序列切分为最长游程。
func SplitRunes(rs []rune) []Run {
	if len(rs) == 0 {
		return nil
	}
	out := make([]Run, 0, len(rs))
	cur := Run{Sym: rs[0], N: 1}
	for _, r := range rs[1:] {
		if r == cur.Sym {
			cur.N++
			continue
		}
		out = append(out, cur)
		cur = Run{Sym: r, N: 1}
	}
	return append(out, cur)
}

// AppendCount 将次数 n 的无前导零十进制表示追加到 dst。
func AppendCount(dst []byte, n int) []byte {
	return strconv.AppendInt(dst, int64(n), 10)
}

// AddDigit 在次数 v 后追加一位数字 d；超过 MaxCount 时 ok=false，且不溢出。
func AddDigit(v int, d byte) (int, bool) {
	digit := int(d - '0')
	if v > (MaxCount-digit)/10 {
		return 0, false
	}
	return v*10 + digit, true
}
