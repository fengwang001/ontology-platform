// Package lex 把十进制字面量切成（符号、整数部分、小数部分、循环节），
// 并做语法/词法校验。本包不依赖工程内其他包。
package lex

import "errors"

// 可判定哨兵错误：词法层三类互不相同；数值溢出由 conv 层报出。
var (
	ErrEmpty       = errors.New("lex: empty input")
	ErrIllegalChar = errors.New("lex: character outside 0-9 . - ( )")
	ErrSyntax      = errors.New("lex: malformed decimal literal")
)

// Parts 是切好的四段。Int 至少 1 位数字；Frac 可为空（j=0）；
// Rep 非空表示存在循环节（k=len(Rep)），且按文法它必在末尾。
type Parts struct {
	Neg  bool
	Int  string
	Frac string
	Rep  string
}

// Scan 为纯函数：输入非法时返回零值 Parts 与哨兵错误，绝不修改任何外部状态。
func Scan(s string) (Parts, error) {
	if s == "" {
		return Parts{}, ErrEmpty
	}
	var p Parts
	i := 0
	if s[0] == '-' {
		p.Neg = true
		i = 1
	}
	if i == len(s) { // 单独一个 "-"
		return Parts{}, ErrSyntax
	}

	const (
		stInt   = 0 // 正在读整数部分
		stFrac  = 1 // 小数点后，正在读 F
		stRep   = 2 // '(' 之后，正在读循环节
		stAfter = 3 // ')' 之后，只允许结束
	)
	state, intStart, fracStart, repStart := stInt, i, 0, 0
	dot, paren := false, false

	for ; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= '0' && c <= '9':
			if state == stAfter {
				return Parts{}, ErrSyntax // ')' 后还有字符
			}
		case c == '.':
			if state != stInt || dot {
				return Parts{}, ErrSyntax // 多个 '.' 或小数点位置非法
			}
			p.Int = s[intStart:i]
			dot, fracStart, state = true, i+1, stFrac
		case c == '(':
			if state == stRep || state == stAfter || paren {
				return Parts{}, ErrSyntax // 多个循环节
			}
			if state == stInt {
				p.Int = s[intStart:i]
			} else {
				p.Frac = s[fracStart:i]
			}
			paren, repStart, state = true, i+1, stRep
		case c == ')':
			if state != stRep {
				return Parts{}, ErrSyntax
			}
			p.Rep = s[repStart:i]
			if p.Rep == "" {
				return Parts{}, ErrSyntax // 空循环节 ()
			}
			state = stAfter
		case c == '-':
			return Parts{}, ErrSyntax // '-' 不在首位
		default:
			return Parts{}, ErrIllegalChar
		}
	}

	switch state {
	case stInt:
		p.Int = s[intStart:]
	case stFrac:
		p.Frac = s[fracStart:]
		if p.Frac == "" {
			return Parts{}, ErrSyntax // 拖尾的 '.'，小数部分缺失
		}
	case stRep:
		return Parts{}, ErrSyntax // '(' 未闭合
	}
	if p.Int == "" {
		return Parts{}, ErrSyntax // 整数部分为空（如 ".5"、"-.5"）
	}
	return p, nil
}
