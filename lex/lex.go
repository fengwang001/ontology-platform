// Package lex 把表达式字符串切成记号流。
// NUMBER 附带 int64 值，其它记号只保留类型。本包不依赖任何其它包。
package lex

import (
	"errors"
	"strconv"
)

// Kind 是记号类型。
type Kind int

const (
	NUMBER Kind = iota
	PLUS
	MINUS
	STAR
	SLASH
	LPAREN
	RPAREN
	EOF
)

// Token 是一个词法记号。
type Token struct {
	Kind Kind
	Val  int64 // 仅 NUMBER 有效
}

// 哨兵错误：词法层可判定故障，互不相同。
var (
	// ErrIllegalChar 出现 NUMBER、+ - * / ( )、空格之外的字符。
	ErrIllegalChar = errors.New("lex: illegal character")
	// ErrNumberOverflow NUMBER 越出 int64 可表示范围。
	ErrNumberOverflow = errors.New("lex: number out of int64 range")
)

// Tokenize 把 s 切成记号流，自动跳过空格，末尾追加一个 EOF。
// 词法非法或数字越界时整体失败，不返回部分结果。
func Tokenize(s string) ([]Token, error) {
	toks := make([]Token, 0, len(s)+1)
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		case c >= '0' && c <= '9':
			j := i
			for j < len(s) && s[j] >= '0' && s[j] <= '9' {
				j++
			}
			v, err := strconv.ParseInt(s[i:j], 10, 64)
			if err != nil {
				return nil, ErrNumberOverflow
			}
			toks = append(toks, Token{Kind: NUMBER, Val: v})
			i = j
		case c == '+':
			toks, i = append(toks, Token{Kind: PLUS}), i+1
		case c == '-':
			toks, i = append(toks, Token{Kind: MINUS}), i+1
		case c == '*':
			toks, i = append(toks, Token{Kind: STAR}), i+1
		case c == '/':
			toks, i = append(toks, Token{Kind: SLASH}), i+1
		case c == '(':
			toks, i = append(toks, Token{Kind: LPAREN}), i+1
		case c == ')':
			toks, i = append(toks, Token{Kind: RPAREN}), i+1
		default:
			return nil, ErrIllegalChar
		}
	}
	toks = append(toks, Token{Kind: EOF})
	return toks, nil
}
