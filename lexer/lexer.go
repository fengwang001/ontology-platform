// Package lexer 是可暂停、可续传的逐字节 CSV 状态机。
package lexer

import (
	"errors"
	"fmt"

	"ontology/cell"
)

// 四类语法错误与终态/上限哨兵。
var (
	ErrBareQuote     = errors.New("bare quote in unquoted field")
	ErrQuoteJunk     = errors.New("unexpected char after closing quote")
	ErrUnclosedQuote = errors.New("unclosed quoted field")
	ErrLoneCR        = errors.New("lone carriage return")
	ErrLimited       = errors.New("parser limit exceeded")
	ErrTerminal      = errors.New("lexer in terminal state")
)

// LimitKind 标识命中的上限种类。
type LimitKind string

const (
	LimitFieldBytes LimitKind = "field bytes"
	LimitFields     LimitKind = "fields per record"
	LimitRecords    LimitKind = "records"
)

// LimitError 是可判定的上限错误。
type LimitError struct{ Kind LimitKind }

func (e *LimitError) Error() string { return string(e.Kind) + " limit exceeded" }
func (e *LimitError) Unwrap() error { return ErrLimited }

// Error 携带字节偏移、记录号、字段号（均从 1 起；偏移从 0 起）。
type Error struct {
	Err    error
	Offset int
	Record int
	Field  int
}

func (e *Error) Error() string {
	return fmt.Sprintf("%v at offset %d (record %d field %d)", e.Err, e.Offset, e.Record, e.Field)
}
func (e *Error) Unwrap() error { return e.Err }

// Handler 接收状态机事件。SkipLine 表示空行被跳过。
type Handler interface {
	Field(c cell.Cell)
	Record()
	SkipLine()
}

type state int

const (
	stStart state = iota
	stBare
	stQuoted
	stQuote
	stCR
)

// Config 配置上限与全局坐标基准（par 分段使用）。
type Config struct {
	Limits        cell.Limits
	BaseOffset    int
	BaseRecord    int
	BaseField     int
	EnforceLimits bool
	StartInside   bool
	NoEOF         bool
}

// Lexer 是状态机实例。单个实例不要求并发安全。
type Lexer struct {
	cfg      Config
	h        Handler
	st       state
	buf      []byte
	off      int
	rec      int
	field    int
	fstart   int
	quoted   bool
	emitted  bool
	terminal bool
	cbErr    error
	bytes    int64
}

// New 创建状态机。
func New(cfg Config, h Handler) *Lexer {
	l := &Lexer{cfg: cfg, h: h, fstart: -1, off: cfg.BaseOffset, rec: cfg.BaseRecord, field: cfg.BaseField}
	if cfg.StartInside {
		l.st, l.quoted, l.emitted = stQuoted, true, true
	}
	return l
}

// BytesProcessed 返回状态机处理的字节总数。
func (l *Lexer) BytesProcessed() int64 { return l.bytes }

// FailFromHandler 由 Handler 在组装阶段发现错误时调用，使状态机于当前字节停止。
func (l *Lexer) FailFromHandler(e error) { l.cbErr = e }

// Feed 喂入一段数据，可调用任意次。
func (l *Lexer) Feed(p []byte) error { return l.run(p, false) }

// Close 结束流并处理 EOF 动作。
func (l *Lexer) Close() error { return l.run(nil, true) }

func (l *Lexer) failAt(err error, pos int) error {
	return &Error{Err: err, Offset: pos, Record: l.rec + 1, Field: l.field + 1}
}

func (l *Lexer) run(p []byte, eof bool) error {
	if l.terminal {
		return &Error{Err: ErrTerminal, Offset: l.off, Record: l.rec + 1, Field: l.field + 1}
	}
	for _, ch := range p {
		l.bytes++
		if e := l.step(ch); e != nil {
			l.terminal = true
			return e
		}
		l.off++
		if l.cbErr != nil {
			l.terminal = true
			e := l.cbErr
			l.cbErr = nil
			return e
		}
	}
	if eof && !l.cfg.NoEOF {
		if e := l.eof(); e != nil {
			l.terminal = true
			return e
		}
		if l.cbErr != nil {
			l.terminal = true
			e := l.cbErr
			l.cbErr = nil
			return e
		}
	}
	return nil
}
