package ontology

import (
	"fmt"
	"strings"
)

// segKind 是导航段的种类。
type segKind int

const (
	segParent   segKind = iota // ^N  取第 N 父（缺省 1）
	segAncestor                // ~N  沿第一父向上 N 代（缺省 1）
	segReflog                  // @{N} 取引用日志中第 N 次更早的值
	segPeel                    // ^{} 剥去标签直至非标签
	segTree                    // ^{tree} 取提交的树
)

// segment 是一个导航段；n 对 segPeel/segTree 无意义。
type segment struct {
	kind segKind
	n    int
	text string // 原始文本，用于日志与报错
}

// expr 是解析后的表达式：基址 + 依次作用的导航段。
type expr struct {
	base string
	segs []segment
}

// isBaseChar 报告 c 是否为允许出现在基址/引用名中的字符。
func isBaseChar(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' ||
		c == '.' || c == '_' || c == '/' || c == '-'
}

// isValidRefName 报告 s 是否为合法引用名（与表达式基址同一字符集）。
func isValidRefName(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if !isBaseChar(s[i]) {
			return false
		}
	}
	return true
}

// parseNum 从 s[i:] 解析十进制数，返回饱和后的值与结束下标。
func parseNum(s string, i int) (int, int) {
	n := 0
	j := i
	for j < len(s) && s[j] >= '0' && s[j] <= '9' {
		n = n*10 + int(s[j]-'0')
		if n > 1<<30 {
			n = 1 << 30
		}
		j++
	}
	return n, j
}

func invalidf(format string, args ...any) *Error {
	return &Error{Kind: ErrInvalid, Detail: fmt.Sprintf(format, args...), Segment: -1}
}

// parseExpr 解析完整表达式；任何文法问题都报「参数非法」。
// 文法：base ("^" N? | "~" N? | "@{" N "}" | "^{}" | "^{tree}")*，N 为十进制，
// "^" 的 N 必须 >= 1。
func parseExpr(s string) (expr, *Error) {
	if s == "" {
		return expr{}, invalidf("empty expression")
	}
	i := 0
	for i < len(s) && isBaseChar(s[i]) {
		i++
	}
	base := s[:i]
	if base == "" {
		return expr{}, invalidf("empty base in %q", s)
	}
	var segs []segment
	for i < len(s) {
		switch s[i] {
		case '^':
			if i+1 < len(s) && s[i+1] == '{' {
				j := strings.IndexByte(s[i+2:], '}')
				if j < 0 {
					return expr{}, invalidf("unclosed ^{ in %q", s)
				}
				content := s[i+2 : i+2+j]
				switch content {
				case "":
					segs = append(segs, segment{kind: segPeel, text: s[i : i+2+j+1]})
				case "tree":
					segs = append(segs, segment{kind: segTree, text: s[i : i+2+j+1]})
				default:
					return expr{}, invalidf("unknown ^{%s} in %q", content, s)
				}
				i += 2 + j + 1
				continue
			}
			n, j := parseNum(s, i+1)
			if j == i+1 {
				n = 1
			}
			if n == 0 {
				return expr{}, invalidf("parent index must be >= 1 in %q", s)
			}
			segs = append(segs, segment{kind: segParent, n: n, text: s[i:j]})
			i = j
		case '~':
			n, j := parseNum(s, i+1)
			if j == i+1 {
				n = 1
			}
			segs = append(segs, segment{kind: segAncestor, n: n, text: s[i:j]})
			i = j
		case '@':
			if i+1 >= len(s) || s[i+1] != '{' {
				return expr{}, invalidf("stray @ in %q", s)
			}
			j := strings.IndexByte(s[i+2:], '}')
			if j < 0 {
				return expr{}, invalidf("unclosed @{ in %q", s)
			}
			content := s[i+2 : i+2+j]
			if content == "" {
				return expr{}, invalidf("empty @{ } in %q", s)
			}
			for k := 0; k < len(content); k++ {
				if content[k] < '0' || content[k] > '9' {
					return expr{}, invalidf("non-numeric @{%s} in %q", content, s)
				}
			}
			n, _ := parseNum(content, 0)
			segs = append(segs, segment{kind: segReflog, n: n, text: s[i : i+2+j+1]})
			i += 2 + j + 1
		default:
			return expr{}, invalidf("unexpected character %q in %q", s[i], s)
		}
	}
	return expr{base: base, segs: segs}, nil
}
