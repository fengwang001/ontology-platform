// Package cell 表示 CSV 字段值：区分未引号空字段与 ""，并保留原文字节偏移。
package cell

import "errors"

// Cell 是一个字段的解析结果。
type Cell struct {
	// Value 是反转义后的字段内容。引号字段内的逗号、\n、\r\n 原样保留。
	Value string
	// Quoted 为 true 表示原文中该字段由一对双引号包裹（包括 ""）。
	Quoted bool
	// Start 是字段首字节的全局偏移：引号字段为开引号位置；
	// 未引号字段为字段槽起始位置（空字段时 Start==End）。
	Start int
	// End 是字段末尾的排他偏移：引号字段为闭引号后一个字节；
	// 未引号字段为最后一个内容字节之后；空字段时等于 Start。
	End int
}

// Equal 报告两个字段（值、引号标记）是否逐字段相等。
func (c Cell) Equal(o Cell) bool {
	return c.Quoted == o.Quoted && c.Value == o.Value &&
		c.Start == o.Start && c.End == o.End
}

// 四类语法错误与三类上限错误，彼此可用 errors.Is 判定。
var (
	ErrQuoteInField  = errors.New("unquoted field contains a double quote")
	ErrAfterQuote    = errors.New("unexpected character after closing quote")
	ErrUnclosedQuote = errors.New("unexpected EOF: quoted field not closed")
	ErrBareCR        = errors.New("bare carriage return not followed by newline")
	ErrFieldTooLong  = errors.New("field exceeds maximum byte length")
	ErrTooManyFields = errors.New("record exceeds maximum field count")
	ErrTooManyRecs   = errors.New("table exceeds maximum record count")
)

// Error 携带出错位置：字节偏移（从 0 起）、记录号与字段号（从 1 起）。
type Error struct {
	Op    string
	Off   int
	Rec   int
	Field int
	Err   error
}

func (e *Error) Error() string {
	return e.Op + ": csv error at offset " + itoa(e.Off) +
		" record " + itoa(e.Rec) + " field " + itoa(e.Field) + ": " + e.Err.Error()
}
func (e *Error) Unwrap() error { return e.Err }

// Is 支持与上述哨兵错误的 errors.Is 判定。
func (e *Error) Is(target error) bool { return e.Err == target }

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [24]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
