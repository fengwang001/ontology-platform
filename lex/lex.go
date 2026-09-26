// Package lex 把不含空格的表达式字符串切成记号流。
package lex

import (
	"errors"
	"fmt"
	"strconv"
)

// Kind 是记号种类。
type Kind int

const (
	Num    Kind = iota // NUMBER：非负整数
	Add                // +
	Sub                // -
	Mul                // *
	Div                // /
	Pow                // ^
	LParen             // (
	RParen             // )
	EOF                // 输入结束
)

// Token 是一个记号；Val 仅对 Num 有意义。
type Token struct {
	Kind Kind
	Val  int64
}

var (
	// ErrIllegalChar：出现 NUMBER、+ - * / ^ ( ) 与空格之外的字符。
	ErrIllegalChar = errors.New("lex: illegal character")
	// ErrNumRange：NUMBER 越出 int64。
	ErrNumRange = errors.New("lex: number out of int64 range")
)

// Tokenize 把 s 切成记号流，末尾追加一个 EOF 记号。
func Tokenize(s string) ([]Token, error) {
	toks := []Token{}
	i := 0
	for i < len(s) {
		c := s[i]
		switch {
		case c == ' ':
			i++
		case c >= '0' && c <= '9':
			j := i + 1
			for j < len(s) && s[j] >= '0' && s[j] <= '9' {
				j++
			}
			v, err := strconv.ParseInt(s[i:j], 10, 64)
			if err != nil {
				return nil, fmt.Errorf("%w: %s", ErrNumRange, s[i:j])
			}
			toks = append(toks, Token{Kind: Num, Val: v})
			i = j
		case c == '+':
			toks = append(toks, Token{Kind: Add})
			i++
		case c == '-':
			toks = append(toks, Token{Kind: Sub})
			i++
		case c == '*':
			toks = append(toks, Token{Kind: Mul})
			i++
		case c == '/':
			toks = append(toks, Token{Kind: Div})
			i++
		case c == '^':
			toks = append(toks, Token{Kind: Pow})
			i++
		case c == '(':
			toks = append(toks, Token{Kind: LParen})
			i++
		case c == ')':
			toks = append(toks, Token{Kind: RParen})
			i++
		default:
			return nil, fmt.Errorf("%w: %q", ErrIllegalChar, rune(c))
		}
	}
	return append(toks, Token{Kind: EOF}), nil
}
