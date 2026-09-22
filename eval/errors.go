package eval

import (
	"errors"
	"fmt"
)

// Sentinel errors. Callers distinguish error classes with errors.Is.
var (
	ErrEmptyExpression  = errors.New("空表达式")
	ErrIllegalChar      = errors.New("非法字符")
	ErrUnmatchedParen   = errors.New("括号不匹配")
	ErrBadLiteral       = errors.New("非法字面量")
	ErrOverflow         = errors.New("溢出")
	ErrDivideByZero     = errors.New("除零")
	ErrNotRepresentable = errors.New("数值不可精确表示")
)

// PosError carries a 0-based byte position for a classified parse error.
// For ErrUnmatchedParen, Ordinal is the 1-based index of the unmatched
// opening parenthesis among all '(' in the input.
type PosError struct {
	Err     error
	Pos     int
	Ordinal int
}

func (e *PosError) Error() string {
	switch {
	case errors.Is(e.Err, ErrUnmatchedParen) && e.Ordinal > 0:
		return fmt.Sprintf("%s: 第 %d 个开括号未闭合 (位置 %d)",
			e.Err.Error(), e.Ordinal, e.Pos)
	case errors.Is(e.Err, ErrUnmatchedParen):
		return fmt.Sprintf("%s: 多余的右括号 (位置 %d)", e.Err.Error(), e.Pos)
	default:
		return fmt.Sprintf("%s (位置 %d)", e.Err.Error(), e.Pos)
	}
}

func (e *PosError) Unwrap() error { return e.Err }

func posError(err error, pos int) *PosError {
	return &PosError{Err: err, Pos: pos}
}
