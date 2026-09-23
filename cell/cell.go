// Package cell 表示 CSV 字段值、其引号标记与原文字节偏移，并定义可判定错误。
package cell

import "strconv"

// Cell 是一个字段。Value 为反转义后的字段内容；Quoted 记录原文是否带引号；
// [Start,End) 为该字段在原文中的字节区间（带引号时包含首尾两个引号）。
type Cell struct {
	Value  []byte
	Quoted bool
	Start  int
	End    int
}

// Kind 是四类语法错误与三类上限错误、列数错误的可判定种类。
type Kind int

const (
	BareQuote       Kind = iota // 未引号字段中出现 "
	CharsAfterQuote             // 引号闭合后紧跟非法字符
	UnclosedQuote               // 流结束时引号未闭合
	BareCR                      // 孤立 \r（其后不是 \n）
	FieldTooLong                // 单字段超过最大字节数
	TooManyFields               // 单记录字段数超限
	TooManyRecords              // 总记录数超限
	ColumnMismatch              // 记录列数与第一条记录不一致
)

// PosError 携带错误种类与字节偏移、记录号、字段号（从 1 起，偏移从 0 起）。
type PosError struct {
	Kind   Kind
	Offset int
	Record int
	Field  int
}

func (e *PosError) Error() string {
	return names[e.Kind] + " at byte " + strconv.Itoa(e.Offset) +
		", record " + strconv.Itoa(e.Record) + ", field " + strconv.Itoa(e.Field)
}

// Is 使任意坐标的同类错误匹配对应哨兵。
func (e *PosError) Is(target error) bool {
	t, ok := target.(*PosError)
	return ok && t.Kind == e.Kind
}

var names = []string{
	"bare quote",
	"characters after closing quote",
	"unclosed quote",
	"bare carriage return",
	"field too long",
	"too many fields in record",
	"too many records",
	"column count mismatch",
}

// 哨兵错误，配合 errors.Is 判定种类，errors.As 取 *PosError 取坐标。
var (
	ErrBareQuote       = &PosError{Kind: BareQuote}
	ErrCharsAfterQuote = &PosError{Kind: CharsAfterQuote}
	ErrUnclosedQuote   = &PosError{Kind: UnclosedQuote}
	ErrBareCR          = &PosError{Kind: BareCR}
	ErrFieldTooLong    = &PosError{Kind: FieldTooLong}
	ErrTooManyFields   = &PosError{Kind: TooManyFields}
	ErrTooManyRecords  = &PosError{Kind: TooManyRecords}
	ErrColumnMismatch  = &PosError{Kind: ColumnMismatch}
)
