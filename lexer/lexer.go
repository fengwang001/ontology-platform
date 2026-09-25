// Package lexer 是 CSV（RFC 4180 方言）的逐字节状态机，可暂停可续传。
// 单个 Lexer 实例不要求并发安全。
package lexer

import (
	"errors"
	"fmt"

	"ontology/cell"
)

// State 是状态机状态。
type State int

const (
	StateFieldStart State = iota // 字段开始
	StateUnquoted                // 未引号字段中
	StateQuoted                  // 引号字段中
	StateQuoteSeen               // 引号字段中刚见到一个引号
	StateCRPending               // 行尾 CR 待定
)

var (
	ErrQuoteInBare     = errors.New("lexer: quote in unquoted field")
	ErrCharsAfterQuote = errors.New("lexer: chars after closing quote")
	ErrUnclosedQuote   = errors.New("lexer: unclosed quote at EOF")
	ErrBareCR          = errors.New("lexer: bare CR outside quotes")
	ErrFieldTooLarge   = errors.New("lexer: field too large")
	ErrTooManyFields   = errors.New("lexer: too many fields in record")
	ErrTooManyRecords  = errors.New("lexer: too many records")
)

// Error 带出错位置：字节偏移从 0 起，记录号、字段号从 1 起。
type Error struct {
	Kind          error
	Offset        int64
	Record, Field int64
}

func (e *Error) Error() string {
	return fmt.Sprintf("%v (offset %d, record %d, field %d)", e.Kind, e.Offset, e.Record, e.Field)
}

func (e *Error) Unwrap() error { return e.Kind }

// Limits 为可配置上限，0 表示不限制。
type Limits struct{ FieldMax, FieldsMax, RecordsMax int64 }

// Event 是一个词法事件：一个完整字段，或一条记录结束。
type Event struct {
	Cell      cell.Cell
	RecordEnd bool
}

// Handler 消费事件；返回非 nil 错误会使解析进入终态。
type Handler func(Event) error

// Resume 是切点处的续传快照。
type Resume struct {
	State         State
	Val           []byte
	Quoted, Blank bool
	CellStart     int64
	Pos           int64
	Rec, NCell    int64
	CrPos         int64
}

// Lexer 是逐字节状态机。processed 为非导出计数器：字节被处理的总次数。
type Lexer struct {
	on              Handler
	lim             Limits
	st              State
	val             []byte
	quoted, blank   bool
	cellStart, pos  int64
	rec, ncell      int64
	crPos           int64
	err             error
	processed       int64
}

func New(on Handler, lim Limits) *Lexer { return &Lexer{on: on, lim: lim} }

func NewResuming(r Resume, on Handler, lim Limits) *Lexer {
	l := New(on, lim)
	l.st, l.quoted, l.blank = r.State, r.Quoted, r.Blank
	l.val = append(l.val, r.Val...)
	l.cellStart, l.pos, l.rec, l.ncell, l.crPos = r.CellStart, r.Pos, r.Rec, r.NCell, r.CrPos
	return l
}

func (l *Lexer) Snapshot() Resume {
	return Resume{State: l.st, Val: append([]byte(nil), l.val...), Quoted: l.quoted,
		Blank: l.blank, CellStart: l.cellStart, Pos: l.pos, Rec: l.rec, NCell: l.ncell, CrPos: l.crPos}
}

func (l *Lexer) Processed() int64 { return l.processed }

func (l *Lexer) Feed(p []byte) error { return nil }

func (l *Lexer) Close() error { return nil }
