// Package cell 表示 CSV 字段值及其原文位置，不依赖其他包。
package cell

import "errors"

// Cell 是一个字段。Quoted 区分未引号空字段与 ""（加引号的空字段）。
// 字节区间为 [Start, End)，End 是字段末字节的下一偏移。
type Cell struct {
	Value  string
	Quoted bool
	Start  int
	End    int
}

// Kind 标识错误类别，四类语法错误与三类上限、终态错误彼此可区分。
type Kind int

const (
	KindBareCR Kind = iota + 1
	KindQuoteInField
	KindCharsAfterQuote
	KindUnclosedQuote
	KindFieldCount
	KindFieldTooLarge
	KindTooManyFields
	KindTooManyRecords
	KindTerminal
)

// 哨兵错误，errors.Is 可判定。
var (
	ErrBareCR           = errors.New("csv: bare CR not followed by LF")
	ErrQuoteInField     = errors.New("csv: unexpected quote in unquoted field")
	ErrCharsAfterQuote  = errors.New("csv: extra data after closing quote")
	ErrUnclosedQuote    = errors.New("csv: unclosed quoted field")
	ErrFieldCount       = errors.New("csv: field count mismatch")
	ErrFieldTooLarge    = errors.New("csv: field too large")
	ErrTooManyFields    = errors.New("csv: too many fields in record")
	ErrTooManyRecords   = errors.New("csv: too many records")
	ErrTerminal         = errors.New("csv: parser is in terminal error state")
)

// Sentinels 按 Kind 索引，便于从类别取哨兵。
var Sentinels = [...]error{
	KindBareCR:          ErrBareCR,
	KindQuoteInField:    ErrQuoteInField,
	KindCharsAfterQuote: ErrCharsAfterQuote,
	KindUnclosedQuote:   ErrUnclosedQuote,
	KindFieldCount:      ErrFieldCount,
	KindFieldTooLarge:   ErrFieldTooLarge,
	KindTooManyFields:   ErrTooManyFields,
	KindTooManyRecords:  ErrTooManyRecords,
	KindTerminal:        ErrTerminal,
}

// ParseError 携带出错字节偏移（从 0 起）与记录号、字段号（从 1 起）。
type ParseError struct {
	Kind   Kind
	Offset int
	Record int
	Field  int
}

func (e *ParseError) Error() string {
	return Sentinels[e.Kind].Error()
}

func (e *ParseError) Unwrap() error { return Sentinels[e.Kind] }

// NewError 构造定位错误。
func NewError(k Kind, off, rec, fld int) *ParseError {
	return &ParseError{Kind: k, Offset: off, Record: rec, Field: fld}
}
