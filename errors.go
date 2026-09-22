package ontology

import (
	"errors"
	"fmt"
)

// 词法 / 语法错误（可用 errors.Is 区分）。
var (
	ErrEmptyExpr      = errors.New("空表达式")
	ErrIllegalChar    = errors.New("非法字符")
	ErrBadLiteral     = errors.New("非法字面量")
	ErrUnmatchedParen = errors.New("括号不匹配")
)

// 求值期数值错误（可用 errors.Is 区分）。
var (
	ErrOverflow = errors.New("溢出")
	ErrDivZero  = errors.New("除零")
	ErrInexact  = errors.New("数值不可精确表示")
)

// Error 是求值器返回的统一错误类型，携带字节位置与类别。
// Pos 为字节偏移，无定位信息时为 -1。
type Error struct {
	Kind error
	Pos  int
	Msg  string
}

func (e *Error) Error() string {
	if e.Pos >= 0 {
		return fmt.Sprintf("%s（字节位置 %d）：%s", e.Kind, e.Pos, e.Msg)
	}
	return fmt.Sprintf("%s：%s", e.Kind, e.Msg)
}

func (e *Error) Unwrap() error { return e.Kind }

func errAt(kind error, pos int, format string, args ...any) *Error {
	return &Error{Kind: kind, Pos: pos, Msg: fmt.Sprintf(format, args...)}
}

func errPlain(kind error, format string, args ...any) *Error {
	return &Error{Kind: kind, Pos: -1, Msg: fmt.Sprintf(format, args...)}
}
