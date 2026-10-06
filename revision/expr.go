package revision

import (
	"fmt"
	"strings"
)

// SegmentKind 枚举五类导航段。
type SegmentKind int

const (
	SegParent   SegmentKind = iota // ^ 或 ^N：取第 N 父
	SegAncestor                    // ~N：沿第一父向上 N 代
	SegReflog                      // @{N}：引用日志第 N 次更早的值
	SegPeel                        // ^{} 剥标签至非标签
	SegTree                        // ^{tree} 取提交的树
)

// Segment 是一个已解析的导航段。
type Segment struct {
	Kind SegmentKind
	N    int // Parent/Ancestor/Reflog 的序号（1 起）
}

// expression 是切分后的首段文本与导航段序列。
type expression struct {
	head string
	segs []Segment
}

// 支持的导航段文法（^ 与 ~ 之间不允许额外分隔符）：
//
//	^     取第一父（等价 ^1）
//	^N    取第 N 父，N 为正整数
//	~     沿第一父向上一代（等价 ~1）
//	~N    沿第一父向上 N 代，N 为正整数
//	^{}   剥标签直至非标签对象
//	^{tree} 取提交的树
//	@{N}  按引用日志取第 N 次更早的值，N 为非负整数（0 即当前值）
//
// 首段不得为空；允许字符为字母、数字、点、下划线、连字符、斜杠。
func parseExpression(text string) (expression, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return expression{}, invalidErr("empty revision expression", 0)
	}
	i := 0
	for i < len(text) && isHeadChar(text[i]) {
		i++
	}
	if i == 0 {
		return expression{}, invalidErr(
			fmt.Sprintf("invalid character %q in revision head", text[0]), 0)
	}
	exp := expression{head: text[:i]}
	segIdx := 0
	for i < len(text) {
		switch text[i] {
		case '^':
			i++
			if i < len(text) && text[i] == '{' {
				i++
				j := strings.IndexByte(text[i:], '}')
				if j < 0 {
					return expression{}, invalidErr("unterminated ^{...}", segIdx)
				}
				inner := text[i : i+j]
				i += j + 1
				switch inner {
				case "":
					exp.segs = append(exp.segs, Segment{Kind: SegPeel})
				case "tree":
					exp.segs = append(exp.segs, Segment{Kind: SegTree})
				default:
					return expression{}, invalidErr(
						fmt.Sprintf("unsupported brace syntax ^{%s}", inner), segIdx)
				}
			} else {
				n, ni, ok := readPosNumber(text, i)
				if !ok {
					if ni == i {
						exp.segs = append(exp.segs, Segment{Kind: SegParent, N: 1})
					} else {
						return expression{}, invalidErr("parent index must be a positive integer", segIdx)
					}
				} else {
					exp.segs = append(exp.segs, Segment{Kind: SegParent, N: n})
					i = ni
				}
			}
		case '~':
			i++
			n, ni, ok := readPosNumber(text, i)
			if !ok {
				if ni == i {
					exp.segs = append(exp.segs, Segment{Kind: SegAncestor, N: 1})
				} else {
					return expression{}, invalidErr("generation count must be a positive integer", segIdx)
				}
			} else {
				exp.segs = append(exp.segs, Segment{Kind: SegAncestor, N: n})
				i = ni
			}
		case '@':
			i++
			if i >= len(text) || text[i] != '{' {
				return expression{}, invalidErr("expected @{N} reflog syntax", segIdx)
			}
			i++
			n, ni, ok := readNumber(text, i)
			if !ok || ni >= len(text) || text[ni] != '}' {
				return expression{}, invalidErr("reflog syntax must be @{N} with non-negative N", segIdx)
			}
			i = ni + 1
			exp.segs = append(exp.segs, Segment{Kind: SegReflog, N: n})
		default:
			return expression{}, invalidErr(
				fmt.Sprintf("unexpected character %q in navigation suffix", text[i]), segIdx)
		}
		segIdx++
	}
	return exp, nil
}

func isHeadChar(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return true
	}
	switch c {
	case '.', '_', '-', '/':
		return true
	}
	return false
}

// readPosNumber 从 at 起读正整数（至少一位，值 >=1）。
// 返回值、数字后位置；语法错误时 ok=false，ni 为已消费位置。
func readPosNumber(s string, at int) (int, int, bool) {
	i := at
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i == at {
		return 0, at, false
	}
	n := 0
	for k := at; k < i; k++ {
		n = n*10 + int(s[k]-'0')
	}
	if n == 0 {
		return 0, i, false
	}
	return n, i, true
}

// readNumber 读非负整数（至少一位）。
func readNumber(s string, at int) (int, int, bool) {
	i := at
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i == at {
		return 0, at, false
	}
	n := 0
	for k := at; k < i; k++ {
		n = n*10 + int(s[k]-'0')
	}
	return n, i, true
}

func invalidErr(detail string, segIdx int) error {
	return &ResolutionError{Code: ErrInvalid, SegIndex: segIdx, Detail: detail}
}
