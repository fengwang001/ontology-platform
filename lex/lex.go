// Package lex tokenizes an expression string into a flat token stream.
// It depends on no other package.
package lex

import (
	"errors"
	"math"
)

// Kind identifies a lexical token.
type Kind uint8

// Token kinds.
const (
	NUMBER Kind = iota
	PLUS
	MINUS
	STAR
	SLASH
	CARET
	LPAREN
	RPAREN
)

// Token is a single lexical token: NUMBER carries its value in Val.
type Token struct {
	Kind Kind
	Val  int64
	Pos  int
}

// Sentinel errors returned by Lex.
var (
	// ErrIllegalChar is returned for any character outside digits, the
	// operators + - * / ^, parentheses and whitespace.
	ErrIllegalChar = errors.New("lex: illegal character")
	// ErrNumberOverflow is returned when a NUMBER literal does not fit int64.
	ErrNumberOverflow = errors.New("lex: number overflows int64")
)

// Lex splits s into tokens. Whitespace (spaces, tabs) is skipped.
func Lex(s string) ([]Token, error) {
	toks := make([]Token, 0, len(s))
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == ' ' || c == '\t':
			i++
		case c >= '0' && c <= '9':
			start := i
			var v int64
			for i < len(s) && s[i] >= '0' && s[i] <= '9' {
				d := int64(s[i] - '0')
				if v > (math.MaxInt64-d)/10 {
					return nil, ErrNumberOverflow
				}
				v = v*10 + d
				i++
			}
			toks = append(toks, Token{Kind: NUMBER, Val: v, Pos: start})
		default:
			k, ok := opKind(c)
			if !ok {
				return nil, ErrIllegalChar
			}
			toks = append(toks, Token{Kind: k, Pos: i})
			i++
		}
	}
	return toks, nil
}

func opKind(c byte) (Kind, bool) {
	switch c {
	case '+':
		return PLUS, true
	case '-':
		return MINUS, true
	case '*':
		return STAR, true
	case '/':
		return SLASH, true
	case '^':
		return CARET, true
	case '(':
		return LPAREN, true
	case ')':
		return RPAREN, true
	}
	return 0, false
}
