// Package lexer 是可暂停续传的 CSV 逐字节状态机。单实例非并发安全。
package lexer

import (
	"errors"

	"ontology/cell"
)

// 可判定哨兵错误（前三类上限与四类语法错误彼此可区分）。
var (
	ErrQuoteInBare     = errors.New("bare quote in unquoted field")
	ErrCharsAfterQuote = errors.New("unexpected chars after closing quote")
	ErrUnclosedQuote   = errors.New("unterminated quoted field")
	ErrBareCR          = errors.New("bare carriage return")
	ErrFieldTooLarge   = errors.New("field exceeds byte limit")
	ErrTooManyFields   = errors.New("record exceeds field limit")
	ErrTooManyRecords  = errors.New("record count exceeds limit")
	ErrClosed          = errors.New("lexer already closed")
)

// Limits 为三类上限，0 表示不限。
type Limits struct{ MaxFieldBytes, MaxFields, MaxRecords int }

// Error 携带 Offset(绝对字节,0基) 与 Record/Field(1基)。
type Error struct {
	Err           error
	Offset        int
	Record, Field int
}

func (e *Error) Error() string { return e.Err.Error() }
func (e *Error) Unwrap() error { return e.Err }

// Sink 接收字段与记录边界事件。
type Sink interface {
	Field(cell.Cell) error
	EndRecord(offset int) error
}

type state uint8

const (
	stOpen  state = iota // 字段开始/未引号进行中（begun 区分）
	stQuote              // 引号字段内
	stQ2                 // 引号字段内刚见引号
	stCR                 // 行尾 CR 待定
)

type lane struct {
	sink                     Sink
	lim                      Limits
	st                       state
	val                      []byte
	quoted, begun, recOpen   bool
	fstart, fend, off, crPos int
	fieldIdx, recs           int
	err                      *Error
	steps, seedLen           int64
}

func (l *lane) fail(err error, off int) *Error {
	if l.err == nil {
		l.err = &Error{err, off, l.recs + 1, l.fieldIdx + 1}
	}
	return l.err
}

func (l *lane) sinkFail(e error) bool {
	if pe, ok := e.(*Error); ok && e != nil {
		l.err = pe
	} else if e != nil {
		l.fail(e, l.off)
	}
	return e != nil
}

func (l *lane) add(b byte, pos int) bool {
	if l.lim.MaxFieldBytes > 0 && l.seedLen+int64(len(l.val)) >= int64(l.lim.MaxFieldBytes) {
		l.fail(ErrFieldTooLarge, pos)
		return false
	}
	l.val = append(l.val, b)
	return true
}

func (l *lane) emit(pos int) cell.Cell {
	end := pos
	if l.quoted {
		end = l.fend
	}
	c := cell.Cell{Value: string(l.val), Quoted: l.quoted, Start: l.fstart, End: end}
	l.val, l.quoted, l.begun = l.val[:0], false, false
	return c
}

func (l *lane) endRec(pos int) bool {
	if l.lim.MaxRecords > 0 && l.recs >= l.lim.MaxRecords {
		l.fail(ErrTooManyRecords, pos)
		return false
	}
	if l.sinkFail(l.sink.Field(l.emit(pos))) || l.sinkFail(l.sink.EndRecord(pos)) {
		return false
	}
	l.recs, l.fieldIdx, l.recOpen, l.st = l.recs+1, 0, false, stOpen
	return true
}

func (l *lane) delim(pos int) bool {
	if l.sinkFail(l.sink.Field(l.emit(pos))) {
		return false
	}
	l.fieldIdx, l.st, l.begun = l.fieldIdx+1, stOpen, false
	return true
}

// step 是核心状态转移（状态×字节类见 DESIGN.md），每字节恰执行一次。
func (l *lane) step(b byte, pos int) {
	l.steps++
	if l.st == stOpen && !l.begun {
		l.fstart, l.recOpen = pos, true
		if l.lim.MaxFields > 0 && l.fieldIdx >= l.lim.MaxFields {
			l.fail(ErrTooManyFields, pos)
			return
		}
	}
	switch b {
	case '"':
		switch l.st {
		case stOpen:
			if l.begun {
				l.fail(ErrQuoteInBare, pos)
			} else {
				l.quoted, l.st = true, stQuote
			}
		case stQuote:
			l.st, l.fend = stQ2, pos+1
		case stQ2:
			if l.add('"', pos) {
				l.st = stQuote
			}
		default:
			l.crFallback(b, pos)
		}
	case ',':
		if l.st == stOpen || l.st == stQ2 {
			l.delim(pos)
		} else if l.st == stQuote {
			l.add(b, pos)
		} else {
			l.crFallback(b, pos)
		}
	case '\n':
		if l.st == stOpen || l.st == stQ2 {
			l.endRec(pos)
		} else if l.st == stQuote {
			l.add(b, pos)
		} else {
			l.endRec(pos)
		}
	case '\r':
		if l.st == stQuote {
			l.add(b, pos)
		} else if l.st == stCR {
			l.crFallback(b, pos)
		} else {
			l.st, l.crPos = stCR, pos
		}
	default:
		if l.st == stQuote {
			l.add(b, pos)
		} else if l.st == stOpen {
			if l.add(b, pos) {
				l.begun = true
			}
		} else if l.st == stQ2 {
			l.fail(ErrCharsAfterQuote, pos)
		} else {
			l.crFallback(b, pos)
		}
	}
}

// crFallback 处理 CR 待定后下一字节非 \n：CR 入值并报 ErrBareCR。
func (l *lane) crFallback(b byte, pos int) {
	l.fail(ErrBareCR, l.crPos)
}

// Parser 是流式半包解析器：多次 Feed，最后 Close。
type Parser struct{ l lane }

func New(sink Sink, lim Limits) *Parser { return &Parser{l: lane{sink: sink, lim: lim}} }

// Feed 喂入一段字节，可调用任意多次；进入终态后返回同一错误。
func (p *Parser) Feed(b []byte) error {
	l := &p.l
	if l.err != nil {
		return l.err
	}
	base := l.off
	for i := 0; i < len(b); i++ {
		if l.step(b[i], base+i); l.err != nil {
			return l.err
		}
	}
	l.off = base + len(b)
	return nil
}

// Close 结束流并刷新最后一条记录（无尾换行合法）。
func (p *Parser) Close() error {
	l := &p.l
	if l.err != nil {
		return l.err
	}
	switch l.st {
	case stQuote, stQ2:
		l.fail(ErrUnclosedQuote, l.off)
	case stCR:
		l.fail(ErrBareCR, l.crPos)
	}
	if l.err == nil && l.recOpen {
		l.endRec(l.off)
	}
	return l.err
}

func (p *Parser) Steps() int64 { return p.l.steps }
