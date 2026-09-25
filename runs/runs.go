
package runs

import (
	"errors"
	"math"
	"strconv"
)

// Run 表示一个码点游程。
type Run struct {
	Symbol rune
	Count  int64
}

// MaxCount 是允许的最大次数（Go 64 位环境下字符串长度的理论上限）。
const MaxCount int64 = math.MaxInt64

// ErrCountTooLarge 在十进制次数超过 MaxCount 时返回。
var ErrCountTooLarge = errors.New("runs: repeat count exceeds MaxInt64")

// Split 把码点序列切成最长游程；空序列返回空切片。
func Split(p []rune) []Run {
	if len(p) == 0 {
		return []Run{}
	}
	out := make([]Run, 0, len(p))
	cur := p[0]
	n := int64(1)
	for _, r := range p[1:] {
		if r == cur {
			n++
			continue
		}
		out = append(out, Run{cur, n})
		cur, n = r, 1
	}
	out = append(out, Run{cur, n})
	return out
}

// DigitBuffer 逐位累加无前导零的十进制次数，溢出即报错。
type DigitBuffer struct {
	n     int64
	empty bool
}

// Add 追加一位 0-9 数字。
func (b *DigitBuffer) Add(d byte) error {
	digit := int64(d - '0')
	if b.n > (MaxCount-digit)/10 {
		return ErrCountTooLarge
	}
	b.n = b.n*10 + digit
	b.empty = false
	return nil
}

// Count 返回已累加的次数。
func (b *DigitBuffer) Count() int64 { return b.n }

// Empty 是否还没有数字。
func (b *DigitBuffer) Empty() bool { return b.empty }

// AppendCount 追加 n 的无前导零十进制表示。
func AppendCount(buf []byte, n int64) []byte {
	return strconv.AppendInt(buf, n, 10)
}
