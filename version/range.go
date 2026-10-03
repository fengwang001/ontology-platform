package version

import "errors"

// Range 表示客户端声明的闭版本区间。
type Range struct {
	Lo int
	Hi int
}

// ErrSyntax 表示区间字符串不符合文法。
var ErrSyntax = errors.New("version: syntax error in version range")

const (
	minVersion = 1
	maxVersion = 1000
)

// Parse 解析区间文法：
//
//	N    -> [N, N]
//	N-M  -> [N, M]，要求 N <= M
//	N-   -> [N, 1000]
//
// N、M 为不含前导零与符号的十进制数，范围 1..1000，其余一律语法错误。
func Parse(s string) (Range, error) {
	if i := indexByte(s, '-'); i >= 0 {
		lo, ok1 := parseNumber(s[:i])
		if !ok1 {
			return Range{}, ErrSyntax
		}
		tail := s[i+1:]
		if tail == "" {
			return Range{Lo: lo, Hi: maxVersion}, nil
		}
		hi, ok2 := parseNumber(tail)
		if !ok2 || lo > hi {
			return Range{}, ErrSyntax
		}
		return Range{Lo: lo, Hi: hi}, nil
	}
	n, ok := parseNumber(s)
	if !ok {
		return Range{}, ErrSyntax
	}
	return Range{Lo: n, Hi: n}, nil
}

func indexByte(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}

// parseNumber 接受 1..1000 范围内、无前导零、无符号的十进制数字串。
func parseNumber(s string) (int, bool) {
	if s == "" || len(s) > 4 {
		return 0, false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < '0' || c > '9' {
			return 0, false
		}
	}
	if len(s) > 1 && s[0] == '0' {
		return 0, false
	}
	n := 0
	for i := 0; i < len(s); i++ {
		n = n*10 + int(s[i]-'0')
	}
	if n < minVersion || n > maxVersion {
		return 0, false
	}
	return n, true
}
