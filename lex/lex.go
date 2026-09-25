// Package lex 把十进制字符串切成（符号、整数部分、小数部分、循环节）四段，
// 并做语法/词法校验。不依赖其他包。
package lex

import "errors"

var (
	// ErrEmpty 空输入。
	ErrEmpty = errors.New("lex: empty input")
	// ErrBadChar 含 0-9 . - ( ) 之外的字符。
	ErrBadChar = errors.New("lex: illegal character")
	// ErrSyntax 语法非法（多个点、多个循环节、空循环节、) 后有字符、
	// 负号不在首位、整数部分为空等）。
	ErrSyntax = errors.New("lex: syntax error")
)

// Parts 是切分后的四段。Neg 为负号标志，Int 至少 1 位，
// Frac 与 Rep 可为空串（表示没有该部分）。
type Parts struct {
	Neg  bool
	Int  string
	Frac string
	Rep  string
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

// Split 校验并切分 s。任何错误都整体失败，不产生部分结果。
func Split(s string) (Parts, error) {
	var p Parts
	if s == "" {
		return p, ErrEmpty
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !isDigit(c) && c != '.' && c != '-' && c != '(' && c != ')' {
			return p, ErrBadChar
		}
	}
	rest := s
	if rest[0] == '-' {
		p.Neg = true
		rest = rest[1:]
	}
	if i := indexByte(rest, '-'); i >= 0 {
		return p, ErrSyntax // 负号不在首位
	}
	// 循环节：至多一对 ()，且 ) 必须在末尾。
	if i := indexByte(rest, '('); i >= 0 {
		if rest[len(rest)-1] != ')' {
			return p, ErrSyntax // ) 后还有字符或缺 )
		}
		p.Rep = rest[i+1 : len(rest)-1]
		if len(p.Rep) == 0 || !allDigits(p.Rep) {
			return p, ErrSyntax // 空循环节或含非数字
		}
		rest = rest[:i]
	}
	if indexByte(rest, '(') >= 0 || indexByte(rest, ')') >= 0 {
		return p, ErrSyntax // 多个循环节或括号位置非法
	}
	// 整数部分与小数部分：至多一个点。
	frac := ""
	if i := indexByte(rest, '.'); i >= 0 {
		frac = rest[i+1:]
		rest = rest[:i]
		if indexByte(frac, '.') >= 0 {
			return p, ErrSyntax // 多个点
		}
		if !allDigits(frac) {
			return p, ErrSyntax // 小数部分含非数字
		}
	}
	if rest == "" || !allDigits(rest) {
		return p, ErrSyntax // 整数部分为空
	}
	p.Int, p.Frac = rest, frac
	return p, nil
}

func allDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if !isDigit(s[i]) {
			return false
		}
	}
	return true
}

func indexByte(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}
