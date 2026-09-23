// Package lexer 是可暂停续传的逐字节 CSV 状态机。
package lexer

import (
	"errors"

	"ontology/cell"
)

// 四类语法错误（可判定哨兵）。
var (
	ErrBareQuote     = errors.New("lexer: bare '\"' in unquoted field")
	ErrAfterQuote    = errors.New("lexer: unexpected char after closing quote")
	ErrUnclosedQuote = errors.New("lexer: unclosed quoted field at EOF")
	ErrLoneCR        = errors.New("lexer: lone '\\r' not followed by '\\n'")
)

// 上限错误。
var (
	ErrFieldTooLarge = errors.New("lexer: field exceeds MaxFieldBytes")
	ErrTooManyFields = errors.New("lexer: record exceeds MaxFields")
	ErrTooManyRecords = errors.New("lexer: input exceeds MaxRecords")
)

// ErrFieldCount 由 table 层使用（列数不一致）。
var ErrFieldCount = errors.New("table: field count mismatch")

// PosError 带字节偏移、记录号、字段号（均从 1 起；偏移从 0 起）。
type PosError struct {
	Err    error
	Offset int
	Record int
	Field  int
}

func (e *PosError) Error() string { return e.Err.Error() }
func (e *PosError) Unwrap() error { return e.Err }

// Limits 是可配置上限；0 表示不限。
type Limits struct {
	MaxFieldBytes int
	MaxFields     int
	MaxRecords    int
}

// Event 是词法事件：Kind 为 "cell" 或 "record"。
type Event struct {
	Kind   string
	Cell   cell.Cell
	Offset int
}

// Lexer 逐字节状态机。非并发安全。
type Lexer struct {
	processed int
}

// Processed 返回状态机处理过的字节总数。
func (l *Lexer) Processed() int { return l.processed }

// Feed / Close 见实现。
func (l *Lexer) Feed(p []byte) error { return nil }
func (l *Lexer) Close() error        { return nil }
