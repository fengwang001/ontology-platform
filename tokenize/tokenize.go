// Package tokenize 把文档正文切成带序号位置的 token 序列。
package tokenize

import (
	"errors"
	"unicode"
)

// MaxTermRunes 是单个 term 的长度上限（按 rune 计）。
const MaxTermRunes = 64

// ErrTermTooLong 表示某个 token 超过长度上限；调用方必须整体拒绝。
var ErrTermTooLong = errors.New("tokenize: term exceeds max length")

// Token 是一个分词结果；Pos 为从 0 开始的 token 序号。
type Token struct {
	Term string
	Pos  int
}

// Tokenize 按 DESIGN.md 第 2 节的规则表分词。
// 空串与只含分隔符的串均返回空结果（不报错），由调用方区分语义。
func Tokenize(text string) ([]Token, error) {
	rs := []rune(text)
	tokens := make([]Token, 0)
	buf := make([]rune, 0, MaxTermRunes+1)
	flush := func() {
		if len(buf) == 0 {
			return
		}
		tokens = append(tokens, Token{Term: string(buf), Pos: len(tokens)})
		buf = buf[:0]
	}
	for i := 0; i < len(rs); {
		r := rs[i]
		switch {
		case isCJK(r):
			flush()
			tokens = append(tokens, Token{Term: string(unicode.ToLower(r)), Pos: len(tokens)})
			i++
		case isWordRune(r):
			buf = append(buf, unicode.ToLower(r))
			if len(buf) > MaxTermRunes {
				return nil, ErrTermTooLong
			}
			i++
		case (r == '-' || r == '_') && len(buf) > 0 && i+1 < len(rs) && isWordRune(rs[i+1]):
			// 连字符/下划线仅在词内（两侧都有词字符）时并入。
			buf = append(buf, r)
			i++
		default:
			flush()
			i++
		}
	}
	flush()
	return tokens, nil
}

func isWordRune(r rune) bool {
	return unicode.IsDigit(r) || (unicode.IsLetter(r) && !isCJK(r))
}

func isCJK(r rune) bool {
	return unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Hangul)
}
