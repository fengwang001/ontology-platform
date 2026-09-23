// Package lexer 是可暂停续传的逐字节 CSV（RFC 4180 方言）状态机。
package lexer

import (
	"errors"

	"ontology/cell"
)

// Mode 为本段首字节之前的起始模式（供 par 切分续跑）。
type Mode int

const (
	MStart   Mode = iota // 引号外、新字段开始
	MQuoted              // 已在引号字段内（携带前缀与语义长度）
	MQPend               // 引号字段内刚读到悬空引号
	MBare                // 未引号字段中间（携带前缀与语义长度）
	MBareCR              // 未引号字段末尾有悬空 CR
	MQPendCR             // 引号字段已闭合且有悬空 CR
)

// 状态机内部状态。
const (
	stField = iota
	stBare
	stQuoted
	stQPending
	stCR
)

// Handler 接收词法事件；field/recNo 为段内序号（从 1 起）。
type Handler interface {
	Cell(c cell.Cell, field int)
	EndRecord(recNo int)
}

// Error 带出错位置（偏移从 0 起，记录/字段从 1 起）。
type Error struct {
	Kind   error
	Offset int64
	Record int
	Field  int
}

func (e *Error) Error() string { return e.Kind.Error() }
func (e *Error) Unwrap() error { return e.Kind }

// 可判定哨兵错误。
var (
	ErrBareQuote     = errors.New("lexer: bare quote in unquoted field")
	ErrQuoteClose    = errors.New("lexer: char after closing quote")
	ErrUnclosedQuote = errors.New("lexer: unclosed quoted field")
	ErrBareCR        = errors.New("lexer: bare carriage return")
	ErrFieldTooLong  = errors.New("lexer: field exceeds byte limit")
	ErrTooManyFields = errors.New("lexer: record exceeds field limit")
	ErrTooManyRecs   = errors.New("lexer: record count exceeds limit")
	ErrTerminal      = errors.New("lexer: terminal state")
)

// Lexer 是单遍状态机；单个实例非并发安全。
type Lexer struct {
	h         Handler
	lim       cell.Limits
	st        int
	val       []byte
	flen      int64
	fstart    int64
	rstart    int64
	pos       int64 // 下一个待消费字节的全局偏移
	rec       int
	field     int
	open      bool
	quotedCR  bool // CR 待定时字段是否已闭合（引号字段）
	fieldDone bool // 进入 CR 待定时字段是否已落定
	bytes     int64
	fatal     *Error
}

// New 创建从流起点开始的词法器。
func New(h Handler, lim cell.Limits) *Lexer {
	return &Lexer{h: h, lim: lim, field: 1}
}

// NewAt 创建按指定模式从 base 偏移启动的词法器。
// prefix/flen 为携带进段的字段前缀；fstart 为携带字段的原始起点（无携带时忽略）。
func NewAt(m Mode, h Handler, lim cell.Limits, base int64, prefix string, flen int64, fstart int64) *Lexer {
	l := New(h, lim)
	l.pos, l.rstart, l.open = base, base, true
	l.val, l.flen = []byte(prefix), flen
	if fstart >= 0 {
		l.fstart = fstart
	}
	switch m {
	case MQuoted:
		l.st = stQuoted
		if fstart < 0 {
			l.fstart = base
		}
	case MQPend, MQPendCR:
		if m == MQPend {
			l.val, l.flen = append(l.val, '"'), flen+1
			l.st = stQPending
		} else {
			l.st, l.fieldDone, l.quotedCR = stCR, true, true
			l.emit(true, fstart, base-1)
		}
	case MBare:
		l.st = stBare
		if fstart < 0 {
			l.fstart = base
		}
	case MBareCR:
		l.st = stCR
		if fstart < 0 {
			l.fstart = base
		}
	}
	return l
}

// Fatal 返回终态错误。
func (l *Lexer) Fatal() *Error { return l.fatal }

// BytesSeen 返回被状态机处理的字节总数。
func (l *Lexer) BytesSeen() int64 { return l.bytes }

// State 返回下一段应使用的启动模式、当前字段前缀、语义长度、字段起点。
func (l *Lexer) State() (Mode, string, int64, int64) {
	switch l.st {
	case stQuoted:
		return MQuoted, string(l.val), l.flen, l.fstart
	case stQPending:
		return MQPend, string(l.val), l.flen, l.fstart
	case stBare:
		return MBare, string(l.val), l.flen, l.fstart
	case stCR:
		if l.quotedCR {
			return MQPendCR, string(l.val), l.flen, l.fstart
		}
		return MBareCR, string(l.val), l.flen, l.fstart
	}
	return MStart, "", 0, l.fstart
}

func (l *Lexer) fail(kind error, off int64) *Error {
	if l.fatal == nil {
		l.fatal = &Error{Kind: kind, Offset: off, Record: l.rec, Field: l.field}
	}
	return l.fatal
}

func (l *Lexer) emit(quoted bool, start, end int64) {
	if l.h != nil {
		l.h.Cell(cell.Cell{Value: string(l.val), Quoted: quoted, Start: start, End: end}, l.field)
	}
	l.val, l.flen = nil, 0
	l.field++
}

func (l *Lexer) closeRec() bool {
	if l.h != nil {
		l.h.EndRecord(l.rec)
	}
	l.field, l.open = 1, false
	return true
}

func (l *Lexer) grow(b byte) bool {
	l.flen++
	if l.lim.MaxFieldBytes > 0 && l.flen > l.lim.MaxFieldBytes {
		l.fail(ErrFieldTooLong, l.pos)
		return false
	}
	l.val = append(l.val, b)
	return true
}

func (l *Lexer) comma() bool {
	if l.lim.MaxFields > 0 && int64(l.field) >= l.lim.MaxFields {
		l.fail(ErrTooManyFields, l.pos)
		return false
	}
	return true
}

func (l *Lexer) recLimit() bool {
	if l.lim.MaxRecords > 0 && int64(l.rec) > l.lim.MaxRecords {
		l.fail(ErrTooManyRecs, l.pos-1)
		return true
	}
	return false
}

func (l *Lexer) startRec() {
	if !l.open {
		l.open = true
		l.rstart = l.pos
		l.rec++
		l.field = 1
	}
}

// Feed 送入一段输入，可任意次调用。
func (l *Lexer) Feed(p []byte) error {
	if l.fatal != nil {
		return errors.Join(ErrTerminal, l.fatal)
	}
	for _, b := range p {
		l.bytes++
		at := l.pos
		l.pos++
		l.startRec()
		switch l.st {
		case stField:
			l.fstart = at
			switch {
			case b == ',':
				if !l.comma() {
					return l.fatal
				}
				l.emit(false, at, at)
			case b == '"':
				l.st = stQuoted
			case b == '\n':
				l.emit(false, at, at)
				if l.recLimit() {
					return l.fatal
				}
				l.closeRec()
			case b == '\r':
				l.st = stCR
			default:
				l.st = stBare
				if !l.grow(b) {
					return l.fatal
				}
			}
		case stBare:
			switch {
			case b == ',':
				if !l.comma() {
					return l.fatal
				}
				l.emit(false, l.fstart, at)
				l.st = stField
			case b == '"':
				return l.fail(ErrBareQuote, at)
			case b == '\n':
				l.emit(false, l.fstart, at)
				l.st = stField
				if l.recLimit() {
					return l.fatal
				}
				l.closeRec()
			case b == '\r':
				l.st, l.fieldDone = stCR, false
			default:
				if !l.grow(b) {
					return l.fatal
				}
			}
		case stQuoted:
			if b == '"' {
				l.st = stQPending
			} else if !l.grow(b) {
				return l.fatal
			}
		case stQPending:
			switch {
			case b == '"':
				l.st = stQuoted
				if !l.grow('"') {
					return l.fatal
				}
			case b == ',':
				if !l.comma() {
					return l.fatal
				}
				l.emit(true, l.fstart, at)
				l.st = stField
			case b == '\n':
				l.emit(true, l.fstart, at)
				l.st = stField
				if l.recLimit() {
					return l.fatal
				}
				l.closeRec()
			case b == '\r':
				l.emit(true, l.fstart, at)
				l.st, l.fieldDone, l.quotedCR = stCR, true, true
			default:
				return l.fail(ErrQuoteClose, at)
			}
		case stCR:
			if b == '\n' {
				if !l.fieldDone {
					l.emit(false, l.fstart, at-1)
				}
				l.quotedCR, l.fieldDone = false, false
				l.st = stField
				if l.recLimit() {
					return l.fatal
				}
				l.closeRec()
			} else {
				return l.fail(ErrBareCR, at-1)
			}
		}
		if l.fatal != nil {
			return l.fatal
		}
	}
	return nil
}

// Close 结束流。
func (l *Lexer) Close() error {
	if l.fatal != nil {
		return errors.Join(ErrTerminal, l.fatal)
	}
	switch l.st {
	case stQuoted:
		return l.fail(ErrUnclosedQuote, l.fstart)
	case stCR:
		return l.fail(ErrBareCR, l.pos-1)
	case stQPending:
		l.emit(true, l.fstart, l.pos)
	case stBare:
		l.emit(false, l.fstart, l.pos)
	case stField:
		if l.open {
			l.emit(false, l.fstart, l.pos)
		}
	}
	if l.open && l.fatal == nil {
		if l.recLimit() {
			return l.fatal
		}
		l.closeRec()
	}
	if l.fatal != nil {
		return l.fatal
	}
	return nil
}
