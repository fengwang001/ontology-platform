// Package scan 逐字符分类括号串中的字符并报告位置。
package scan

import "errors"

// Kind 是单个字符的分类。
type Kind int

const (
	// Other 既不是左括号也不是右括号。
	Other Kind = iota
	// Left 是左括号 '('。
	Left
	// Right 是右括号 ')'。
	Right
)

// ErrIllegalChar 表示出现了非 '(' 与 ')' 的字符。
var ErrIllegalChar = errors.New("scan: illegal character")

// IllegalCharError 携带第一个非法字符的下标。
type IllegalCharError struct {
	Index int
	Char  byte
}

func (e *IllegalCharError) Error() string { return "scan: illegal character" }

// Is 让 errors.Is 命中 ErrIllegalChar。
func (e *IllegalCharError) Is(target error) bool { return target == ErrIllegalChar }

// Classify 判定位置 i 处字符的类别；非法字符返回携带下标的错误。
func Classify(s string, i int) (Kind, error) {
	if i < 0 || i >= len(s) {
		return Other, &IllegalCharError{Index: i}
	}
	switch s[i] {
	case '(':
		return Left, nil
	case ')':
		return Right, nil
	default:
		return Other, &IllegalCharError{Index: i, Char: s[i]}
	}
}
