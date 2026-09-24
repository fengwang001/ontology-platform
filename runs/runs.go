// Package runs 把码点序列切成最长游程，并提供溢出安全的十进制次数读写。
// 它不依赖工程中的其他包。
package runs

import "math"

// Run 表示同一个码点连续出现 Count 次。
type Run struct {
	Symbol rune
	Count  int
}

// Split 以回调方式产出 s 中的最长游程。回调返回 false 时提前停止。
// 无效 UTF-8 字节按 RuneError 各自成一个游程，由调用方决定是否接受。
func Split(s string, fn func(Run) bool) {
	var r Run
	set := false
	flush := func() bool {
		if set {
			ok := fn(r)
			set = false
			return ok
		}
		return true
	}
	for _, c := range s {
		if set && c == r.Symbol {
			r.Count++
			continue
		}
		if !flush() {
			return
		}
		r = Run{Symbol: c, Count: 1}
		set = true
	}
	flush()
}

// MaxCount 是允许的最大次数。超过即视为溢出。
const MaxCount = math.MaxInt

// ErrCountTooLarge 表示十进制次数超过 MaxCount（或非正）。
type ErrCountTooLarge struct{}

func (ErrCountTooLarge) Error() string { return "runs: run count exceeds MaxInt" }

// AppendCount 把次数（Count >= 1）的无前导零十进制表示追加到 dst。
func AppendCount(dst []byte, count int) []byte {
	var buf [20]byte
	i := len(buf)
	for count > 0 {
		i--
		buf[i] = byte('0' + count%10)
		count /= 10
	}
	return append(dst, buf[i:]...)
}

// AddDigit 在已有累计值 n 后追加十进制数字 d 并做溢出检查。
// 结果超过 MaxCount 时返回 ErrCountTooLarge，绝不回绕。
func AddDigit(n int, d byte) (int, error) {
	v := int(d - '0')
	if n > (MaxCount-v)/10 {
		return 0, ErrCountTooLarge{}
	}
	return n*10 + v, nil
}
