// Package lexer 是逐字节、可暂停续传的 CSV（RFC 4180 方言）词法状态机。
package lexer

import (
	"errors"

	"ontology/cell"
)

var (
	ErrBareQuote      = errors.New("bare '\"' in unquoted field")
	ErrQuoteAfter     = errors.New("unexpected character after closing quote")
	ErrUnclosedQuote  = errors.New("unclosed quoted field at end of input")
	ErrBareCR         = errors.New("bare '\\r' not followed by '\\n'")
	ErrFieldTooLong   = errors.New("field exceeds maximum bytes")
	ErrTooManyFields  = errors.New("record exceeds maximum fields")
	ErrTooManyRecords = errors.New("input exceeds maximum records")
)

// Error 携带字节偏移（0 起）、记录号、字段号（均 1 起）。
type Error struct{ Err error; Offset int64; Record, Field int }

func (e *Error) Error() string { return e.Err.Error() }
func (e *Error) Unwrap() error { return e.Err }

// Sink 接收词法事件；fieldEnd 是字段内容区之后偏移，recEnd 是终止符之后偏移。
type Sink interface {
	Field(c cell.Cell, recNo, fieldNo int, fieldEnd int64) error
	EndRecord(recNo int, recEnd int64) error
}

// Options 配置上限与起点；上限为 0 表示不启用。
type Options struct {
	MaxFieldBytes int64
	MaxFields     int
	StartOffset   int64
	OnNewline     func(offset int64)
}

// 状态：字段开始 / 未引号字段 / 引号字段 / 引号字段中刚见引号 / 行尾 CR 待定。
const (
	stStart = iota
	stUnquoted
	stQuoted
	stQSeen
	stCR
)

// Lexer 是流式状态机。单实例非并发安全；多个实例可并发使用。
type Lexer struct {
	sink                      Sink
	opts                      Options
	state                     int
	off, start, qEnd, crOff   int64
	rec, field                int
	buf                       []byte
	vlen                      int64
	quotd, crQ, crBlank       bool
	fatal                     error
	processed                 int64
}

// New 创建状态机。
func New(sink Sink, opts Options) *Lexer {
	return &Lexer{sink: sink, opts: opts, off: opts.StartOffset, rec: 1}
}

// Processed 返回字节被状态机处理的总次数。
func (l *Lexer) Processed() int64 { return l.processed }

func (l *Lexer) fail(err error, off int64) error {
	e := &Error{Err: err, Offset: off, Record: l.rec, Field: l.field}
	l.fatal = e
	return e
}

func (l *Lexer) emit(end int64) error {
	var c cell.Cell
	if l.quotd {
		c = cell.NewQuoted(string(l.buf), l.start, end)
	} else {
		c = cell.New(string(l.buf), l.start, end)
	}
	if err := l.sink.Field(c, l.rec, l.field, end); err != nil {
		l.fatal = err
		return err
	}
	return nil
}

// grow 接收一个字段值字节；引号转义 "" 在此共追加一个引号。
func (l *Lexer) grow(b byte) error {
	l.buf = append(l.buf, b)
	l.vlen++
	if l.opts.MaxFieldBytes > 0 && l.vlen > l.opts.MaxFieldBytes {
		return l.fail(ErrFieldTooLong, l.off)
	}
	return nil
}

// nextSlot 发射当前字段（endAt 为其后偏移）并开始下一槽位。
func (l *Lexer) nextSlot(endAt int64) error {
	if err := l.emit(endAt); err != nil {
		return err
	}
	if l.opts.MaxFields > 0 && l.field+1 > l.opts.MaxFields {
		return l.fail(ErrTooManyFields, endAt)
	}
	l.field++
	l.start, l.buf, l.vlen, l.quotd = l.off, l.buf[:0], 0, false
	l.state = stStart
	return nil
}

// endRec 发射当前字段（endAt）、终止记录；crBlank 时跳过空行。
func (l *Lexer) endRec(endAt, after int64) error {
	if l.crBlank {
		l.crBlank = false
		if l.opts.OnNewline != nil {
			l.opts.OnNewline(after)
		}
		l.rec++
		l.state = stStart
		return nil
	}
	if err := l.emit(endAt); err != nil {
		return err
	}
	if l.opts.OnNewline != nil {
		l.opts.OnNewline(after)
	}
	if err := l.sink.EndRecord(l.rec, after); err != nil {
		l.fatal = err
		return err
	}
	l.rec++
	l.field = 0
	l.state = stStart
	return nil
}

// Feed 喂入一段字节；出错即进入终态，之后恒返回同一错误。
func (l *Lexer) Feed(p []byte) error {
	if l.fatal != nil {
		return l.fatal
	}
	for _, b := range p {
		l.processed++
		at := l.off
		l.off++
		if l.state == stCR { // 唯一待定态：非 \n 即在 CR 处错误
			if b != '\n' {
				return l.fail(ErrBareCR, l.crOff)
			}
			if err := l.endRec(func() int64 {
				if l.crQ {
					l.crQ = false
					return l.qEnd
				}
				return l.crOff
			}(), l.off); err != nil {
				return err
			}
			continue
		}
		switch l.state {
		case stStart:
			switch {
			case b == ',' && l.field == 0:
				l.field = 1
				if err := l.nextSlot(at); err != nil {
					return err
				}
			case b == ',':
				if err := l.nextSlot(at); err != nil {
					return err
				}
			case b == '"':
				if l.field == 0 {
					l.field, l.start = 1, at
				}
				l.quotd, l.state = true, stQuoted
			case b == '\r':
				l.crOff = at
				l.crBlank = l.field == 0
				l.state = stCR
			case b == '\n' && l.field == 0: // 空行（无 CR）
				if l.opts.OnNewline != nil {
					l.opts.OnNewline(l.off)
				}
				l.rec++
			case b == '\n':
				if err := l.endRec(at, l.off); err != nil {
					return err
				}
			default:
				if l.field == 0 {
					l.field, l.start = 1, at
				}
				if err := l.grow(b); err != nil {
					return err
				}
				l.state = stUnquoted
			}
		case stUnquoted:
			switch {
			case b == ',':
				if err := l.nextSlot(at); err != nil {
					return err
				}
			case b == '"':
				return l.fail(ErrBareQuote, at)
			case b == '\r':
				l.crOff, l.state = at, stCR
			case b == '\n':
				if err := l.endRec(at, l.off); err != nil {
					return err
				}
			default:
				if err := l.grow(b); err != nil {
					return err
				}
			}
		case stQuoted:
			if b == '"' {
				l.qEnd, l.state = l.off, stQSeen
			} else if err := l.grow(b); err != nil {
				return err
			}
		case stQSeen:
			switch {
			case b == '"':
				l.state = stQuoted
				if err := l.grow('"'); err != nil {
					return err
				}
			case b == ',':
				if err := l.nextSlot(l.qEnd); err != nil {
					return err
				}
			case b == '\r':
				l.crOff, l.crQ, l.state = at, true, stCR
			case b == '\n':
				if err := l.endRec(l.qEnd, l.off); err != nil {
					return err
				}
			default:
				return l.fail(ErrQuoteAfter, at)
			}
		}
	}
	return nil
}

// Close 结束输入：未闭合引号、裸 CR 报错；无换行结尾的末记录合法。
func (l *Lexer) Close() error {
	if l.fatal != nil {
		return l.fatal
	}
	if l.state == stQuoted {
		return l.fail(ErrUnclosedQuote, l.off)
	}
	if l.state == stCR {
		return l.fail(ErrBareCR, l.crOff)
	}
	if l.state == stStart && l.field == 0 {
		return nil
	}
	if l.state == stUnquoted || l.state == stStart {
		if err := l.emit(l.off); err != nil {
			return err
		}
	}
	if l.state == stQSeen {
		if err := l.emit(l.qEnd); err != nil {
			return err
		}
	}
	if err := l.sink.EndRecord(l.rec, l.off); err != nil {
		l.fatal = err
		return err
	}
	return nil
}
