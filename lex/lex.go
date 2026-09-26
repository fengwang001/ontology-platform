// Package lex 把表达式字符串切成记号流。不依赖其它包。
package lex

import (
	"errors"
	"fmt"
	"strconv"
)

// ErrLexical 词法非法：出现非法字符，或 NUMBER 越出 int64 可表示范围。
var ErrLexical = errors.New("lex: illegal character or number out of int64 range")

// Kind 是记号类型。
type Kind int

const (
	Number Kind = iota // 非负整数字面量，值在 Token.Val
	Add                // +
	Sub                // -
	Mul                // *
	Div                // /
	LParen             // (
	RParen             // )
)

// Token 是一个记号；仅 Number 携带 Val。
type Token struct {
	Kind Kind
	Val  int64
}

// punct 把单字符运算符映射到记号类型。
var punct = map[byte]Kind{
	'+': Add, '-': Sub, '*': Mul, '/': Div, '(': LParen, ')': RParen,
}

// Lex 把 s 切成记号流；跳过空格；遇非法字符或越界数字返回 ErrLexical。
func Lex(s string) ([]Token, error) {
	var toks []Token
	i := 0
	for i < len(s) {
		c := s[i]
		switch {
		case c == ' ':
			i++
		case c >= '0' && c <= '9':
			j := i
			for j < len(s) && s[j] >= '0' && s[j] <= '9' {
				j++
			}
			v, err := strconv.ParseInt(s[i:j], 10, 64)
			if err != nil {
				return nil, fmt.Errorf("%w: %q", ErrLexical, s[i:j])
			}
			toks = append(toks, Token{Kind: Number, Val: v})
			i = j
		case c == '+' || c == '-' || c == '*' || c == '/' || c == '(' || c == ')':
			toks = append(toks, Token{Kind: punct[c]})
			i++
		default:
			return nil, fmt.Errorf("%w: %q", ErrLexical, string(c))
		}
	}
	return toks, nil
}
