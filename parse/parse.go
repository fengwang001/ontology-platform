// Package parse 校验并解析只含普通字符、'.'、'*' 的模式。
// 不依赖其他包。
package parse

import "errors"

// 三类可判定的哨兵错误，互不相同。
var (
	ErrSyntax      = errors.New("parse: 非法模式语法（开头是 * 或连续 **）")
	ErrUnsupported = errors.New("parse: 不支持的字符")
	ErrTooLong     = errors.New("parse: 输入长度超过 maxLen")
)

// MaxLen 是模式与文本允许的最大长度。
const MaxLen = 1 << 16

// Token 是模式的一个元素：Ch 为普通字符或 '.'，Star 表示其后跟了 '*'。
type Token struct {
	Ch   byte
	Star bool
}

func ordinary(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

// Parse 校验 pattern 并切成 Token 序列。
// 任何非法（超长、开头 '*'、连续 '**'、不支持的字符）都整体失败，不产出任何 Token。
func Parse(pattern string) ([]Token, error) {
	if len(pattern) > MaxLen {
		return nil, ErrTooLong
	}
	toks := make([]Token, 0, len(pattern))
	for i := 0; i < len(pattern); i++ {
		c := pattern[i]
		switch {
		case c == '*':
			if len(toks) == 0 || toks[len(toks)-1].Star {
				return nil, ErrSyntax
			}
			toks[len(toks)-1].Star = true
		case c == '.' || ordinary(c):
			toks = append(toks, Token{Ch: c})
		default:
			return nil, ErrUnsupported
		}
	}
	return toks, nil
}
