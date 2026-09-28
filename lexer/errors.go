// Package lexer 是 RFC4180 方言 CSV 的逐字节状态机：可半包续传，
// 也可对一段字节按指定入口状态独立解析（供 par 并行切分复用）。
package lexer

import "errors"

// 入口/出口状态（跨段唯一需要传递的记忆）。
const (
	StStart  = 0
	StPlain  = 1
	StQuoted = 2
	StQSeen  = 3
	StCR     = 4
)

const (
	EvOpen = iota
	EvData
	EvClose
	EvRec
	EvErr
)

// Event 是状态机产出的原子事件，Off 为全局字节偏移。
type Event struct {
	Kind   int
	Off    int
	Quoted bool
	B      byte
	Err    error
}

var (
	ErrBareQuote       = errors.New("bare quote in unquoted field")
	ErrQuoteAfterClose = errors.New("unexpected char after closing quote")
	ErrUnclosedQuote   = errors.New("unterminated quoted field")
	ErrBareCR          = errors.New("bare carriage return")
	ErrFieldTooLong    = errors.New("field too long")
	ErrTooManyFields   = errors.New("too many fields in record")
	ErrTooManyRecords  = errors.New("too many records")
	ErrClosed          = errors.New("parser is in terminal state")
)

// PosError 带字节偏移（从 0 起）、记录号、字段号（从 1 起）。
type PosError struct {
	Err           error
	Offset        int
	Record, Field int
}

func (e *PosError) Error() string { return e.Err.Error() }
func (e *PosError) Unwrap() error { return e.Err }

// Limits 是三类上限；0 表示不限制。
type Limits struct {
	MaxFieldBytes, MaxFields, MaxRecords int
}

// Run 是一段字节在某入口状态下的独立解析结果。
type Run struct {
	Ev      []Event
	Exit    int
	Opened  bool
	Handled int
}
