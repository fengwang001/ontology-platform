package safeexpr

import "errors"

// 哨兵错误：调用方可用 errors.Is 区分错误类别。
var (
	// ErrEmpty 空表达式或纯空白。
	ErrEmpty = errors.New("空表达式")
	// ErrIllegalChar 出现不允许的字符。
	ErrIllegalChar = errors.New("非法字符")
	// ErrMalformedNumber 数字字面量格式非法（如 1.2.3、..5、5.）。
	ErrMalformedNumber = errors.New("非法字面量")
	// ErrUnmatchedParen 括号不匹配。
	ErrUnmatchedParen = errors.New("括号不匹配")
	// ErrSyntax 其他语法错误（缺操作数、意外 token 等）。
	ErrSyntax = errors.New("语法错误")
	// ErrDivisionByZero 除数为 0。
	ErrDivisionByZero = errors.New("除零")
	// ErrOverflow 整数结果超出 int64 范围。
	ErrOverflow = errors.New("溢出")
	// ErrInexact 非整数值无法精确表示为 float64。
	ErrInexact = errors.New("数值不可精确表示")
)

// Error 携带分类与定位信息。Kind 为哨兵错误之一。
type Error struct {
	Kind error
	Msg  string
	// Pos 为从 0 开始的字节位置；不适用时为 -1。
	Pos int
	// OpenIndex 为未闭合/多余开括号的序号（从 1 开始）；不适用时为 0。
	OpenIndex int
}

func (e *Error) Error() string {
	s := e.Kind.Error()
	if e.Msg != "" {
		s += ": " + e.Msg
	}
	if e.Pos >= 0 {
		s += " (位置 " + itoa(e.Pos) + ")"
	}
	if e.OpenIndex > 0 {
		s += " (第 " + itoa(e.OpenIndex) + " 个开括号)"
	}
	return s
}

// Unwrap 让 errors.Is/As 能命中哨兵错误。
func (e *Error) Unwrap() error { return e.Kind }

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [24]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

func newError(kind error, msg string, pos int) *Error {
	return &Error{Kind: kind, Msg: msg, Pos: pos}
}
