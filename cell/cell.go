package cell

import (
	"errors"
	"fmt"
)

// Cell 是一个 CSV 字段：解析后的值、是否加过引号、原文起止字节偏移（End 为开区间）。
type Cell struct {
	Value  string
	Quoted bool
	Start  int
	End    int
}

// Kind 是可判定的错误类别。
type Kind int

const (
	KindBareQuote Kind = iota + 1
	KindQuoteAfterClose
	KindUnterminatedQuote
	KindLoneCR
	KindFieldTooLarge
	KindTooManyFields
	KindTooManyRecords
	KindColumnMismatch
)

func (k Kind) String() string {
	switch k {
	case KindBareQuote:
		return "bare quote in unquoted field"
	case KindQuoteAfterClose:
		return "unexpected character after closing quote"
	case KindUnterminatedQuote:
		return "unterminated quoted field"
	case KindLoneCR:
		return "lone carriage return"
	case KindFieldTooLarge:
		return "field exceeds byte limit"
	case KindTooManyFields:
		return "record exceeds field limit"
	case KindTooManyRecords:
		return "table exceeds record limit"
	case KindColumnMismatch:
		return "record field count mismatch"
	}
	return "unknown csv error"
}

// Error 携带类别与从 1 起的记录号、字段号，以及从 0 起的字节偏移。
type Error struct {
	Kind   Kind
	Offset int
	Record int
	Field  int
}

func (e *Error) Error() string {
	return fmt.Sprintf("csv: %s at byte %d (record %d field %d)", e.Kind, e.Offset, e.Record, e.Field)
}

// Is 支持 errors.Is(err, ErrBareQuote) 等按类别判定。
func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	return ok && t.Kind == e.Kind
}

// NewError 构造带位置的错误；rec/field 为 0 表示尚未有对应编号。
func NewError(k Kind, off, rec, field int) *Error {
	return &Error{Kind: k, Offset: off, Record: rec, Field: field}
}

// 哨兵错误（Kind 已设、位置为零值），供 errors.Is 判定。
var (
	ErrBareQuote         = &Error{Kind: KindBareQuote}
	ErrQuoteAfterClose   = &Error{Kind: KindQuoteAfterClose}
	ErrUnterminatedQuote = &Error{Kind: KindUnterminatedQuote}
	ErrLoneCR            = &Error{Kind: KindLoneCR}
	ErrFieldTooLarge     = &Error{Kind: KindFieldTooLarge}
	ErrTooManyFields     = &Error{Kind: KindTooManyFields}
	ErrTooManyRecords    = &Error{Kind: KindTooManyRecords}
	ErrColumnMismatch    = &Error{Kind: KindColumnMismatch}
)
