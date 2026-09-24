// Package runs 把码点序列切成最长游程，并提供游程序数的十进制解析。
package runs

import (
	"errors"
	"fmt"
	"io"
	"unicode/utf8"
)

// MaxCount 是单个游程允许的最大重复次数（64 位有符号整数上限）。
const MaxCount = int64(^uint64(0) >> 1)

// 次数部分可能出现的错误。
var (
	// ErrCountZero 表示次数被显式写成 0。
	ErrCountZero = errors.New("runs: run count is zero")
	// ErrCountOne 表示次数被显式写成 1（规范形式必须省略）。
	ErrCountOne = errors.New("runs: run count 1 must be omitted")
	// ErrCountLeadingZero 表示十进制次数带前导零。
	ErrCountLeadingZero = errors.New("runs: run count has leading zero")
	// ErrCountOverflow 表示次数超过 MaxCount。
	ErrCountOverflow = errors.New("runs: run count overflows int64")
)

// Scanner 逐字节解析游程开头的十进制次数。
//
// 对游程起始处连续调用 Add；第一个非数字字节表示次数结束（该字节不被消费），
// 随后调用 Validate 与 Count。HasDigits 区分显式次数与省略次数。
type Scanner struct {
	count   int64
	has     bool
	leading bool
	started int64
}

// Add 喂入偏移为 offset 的字节。数字则累积并返回 (true, nil)；
// 超过 MaxCount 返回 ErrCountOverflow；非数字返回 (false, nil) 且不改变状态。
func (s *Scanner) Add(b byte, offset int64) (bool, error) {
	if b < '0' || b > '9' {
		return false, nil
	}
	if !s.has {
		s.has = true
		s.started = offset
		s.leading = b == '0'
	}
	d := int64(b - '0')
	if s.count > (MaxCount-d)/10 {
		return true, fmt.Errorf("%w: at byte offset %d", ErrCountOverflow, offset)
	}
	s.count = s.count*10 + d
	return true, nil
}

// Count 返回解析出的次数；未出现数字时为 1。
func (s *Scanner) Count() int64 {
	if !s.has {
		return 1
	}
	return s.count
}

// Validate 在次数部分结束后检查规范形式：0、前导零、显式 1 均非法。
func (s *Scanner) Validate() error {
	if !s.has {
		return nil
	}
	switch {
	case s.count == 0:
		return fmt.Errorf("%w: at byte offset %d", ErrCountZero, s.started)
	case s.leading:
		return fmt.Errorf("%w: at byte offset %d", ErrCountLeadingZero, s.started)
	case s.count == 1:
		return fmt.Errorf("%w: at byte offset %d", ErrCountOne, s.started)
	}
	return nil
}

// HasDigits 报告是否出现过显式次数数字。
func (s *Scanner) HasDigits() bool { return s.has }

// TokenStart 返回整个游程 token（次数或符号）的起始字节偏移。
func (s *Scanner) TokenStart(symbolOffset int64) int64 {
	if s.has {
		return s.started
	}
	return symbolOffset
}

// Split 将合法 UTF-8 字符串切成最长游程（相邻相同码点合并），逐个写入 w。
// 按定长小块重复写出，分配量与游程次数无关。
func Split(s string, w io.Writer) error {
	var buf [utf8.UTFMax]byte
	var symbol rune
	var count int64
	flush := func() error {
		if count == 0 {
			return nil
		}
		n := utf8.EncodeRune(buf[:], symbol)
		return writeRepeated(w, buf[:n], count)
	}
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == symbol && count > 0 {
			count++
		} else {
			if err := flush(); err != nil {
				return err
			}
			symbol, count = r, 1
		}
		i += size
	}
	return flush()
}

func writeRepeated(w io.Writer, p []byte, count int64) error {
	for count > 0 {
		n := count
		if n > 4096 {
			n = 4096
		}
		for j := int64(0); j < n; j++ {
			if _, err := w.Write(p); err != nil {
				return err
			}
		}
		count -= n
	}
	return nil
}
