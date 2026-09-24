// Package runs 把 Unicode 码点序列切成最长游程，并提供游程次数的十进制读写。
// 本包不依赖工程中的其他包。
package runs

import (
	"errors"
	"math/big"
	"unicode/utf8"
)

// Run 表示同一个码点连续出现 Count 次。
type Run struct {
	Sym   rune
	Count *big.Int
}

var (
	// ErrEmptyCount 表示次数字段不含任何十进制数字。
	ErrEmptyCount = errors.New("runs: empty run count")
	// ErrLeadingZero 表示次数字段带前导零（如 "03"）。
	ErrLeadingZero = errors.New("runs: leading zero in run count")
	// ErrNotDigit 表示次数字段中出现非 ASCII 数字。
	ErrNotDigit = errors.New("runs: non-digit in run count")
)

// Split 将码点序列切成相邻符号互异的最长游程。空输入返回 nil。
func Split(s []rune) []Run {
	if len(s) == 0 {
		return nil
	}
	var out []Run
	sym := s[0]
	n := big.NewInt(1)
	for i := 1; i < len(s); i++ {
		if s[i] == sym {
			n.Add(n, big.NewInt(1))
			continue
		}
		out = append(out, Run{Sym: sym, Count: n})
		sym = s[i]
		n = big.NewInt(1)
	}
	return append(out, Run{Sym: sym, Count: n})
}

// FormatCount 输出次数的规范十进制文本：1 省略为空，>=2 无前导零。
func FormatCount(n *big.Int) string {
	if n.Cmp(big.NewInt(1)) <= 0 {
		return ""
	}
	return n.String()
}

// CountKind 描述一个原始次数字段的规范类别。
type CountKind int

const (
	CountAbsent    CountKind = iota // 无显式次数，隐式 1
	CountCanonical                  // 无前导零
	CountLeading                    // 带前导零（含 "0"）
)

// ParseCount 解析 [start,end) 内的原始十进制次数字段。
// 空区间得到 CountAbsent 且 n=1；数字解析为任意精度整数，绝不溢出。
func ParseCount(text string, start, end int) (n *big.Int, kind CountKind, err error) {
	if start < 0 || end > len(text) || start > end {
		return nil, CountAbsent, ErrEmptyCount
	}
	if start == end {
		return big.NewInt(1), CountAbsent, nil
	}
	kind = CountCanonical
	n = new(big.Int)
	for i := start; i < end; i++ {
		c := text[i]
		if c < '0' || c > '9' {
			return nil, kind, ErrNotDigit
		}
		n.Mul(n, big.NewInt(10))
		n.Add(n, big.NewInt(int64(c-'0')))
	}
	if end-start > 1 && text[start] == '0' {
		kind = CountLeading
	}
	return n, kind, nil
}

// NeedEscape 报告符号在编码文本里是否必须加反斜杠。
func NeedEscape(r rune) bool {
	return r >= 0 && r <= utf8.RuneSelf && (r == '\\' || r >= '0' && r <= '9')
}
