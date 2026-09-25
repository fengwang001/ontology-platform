// Package lexer 是可暂停续传的 CSV(RFC4180 方言) 字节状态机，不缓冲整行。单实例非并发安全。
package lexer

import (
	"errors"
	"fmt"
	"ontology/cell"
)

// 四类语法错误 + 孤立 CR + 字段上限，彼此用 errors.Is 可判定。
var (
	ErrBareQuote       = errors.New("lexer: bare quote in unquoted field")
	ErrQuoteAfterClose = errors.New("lexer: unexpected char after closing quote")
	ErrUnclosedQuote   = errors.New("lexer: unclosed quoted field at EOF")
	ErrOrphanCR        = errors.New("lexer: carriage return not followed by newline")
	ErrFieldTooLong    = errors.New("lexer: field exceeds max bytes")
)

// PosError 携带字节偏移(0起)、记录号、字段号(1起)。
type PosError struct {
	Err                   error
	Offset, Record, Field int
}

func (e *PosError) Error() string {
	return fmt.Sprintf("%v at byte %d, record %d, field %d", e.Err, e.Offset, e.Record, e.Field)
}
func (e *PosError) Unwrap() error { return e.Err }

type Handler interface {
	Field(cell.Cell) error
	EndRecord() error
}

type state uint8

const (
	stF  state = iota // 字段开始
	stB               // 未引号字段中
	stQ               // 引号字段中
	stQE              // 引号中刚见引号
	stCR              // 行尾 CR 待定
)

type Lexer struct {
	h                        Handler
	maxVal, base             int
	state                    state
	val                      []byte
	quoted, recOpen, crField bool
	fStart, rec, fld         int
	bytes                    int64
	fatal                    *PosError
}

// Init 为并行段注入起始状态。
type Init struct {
	InQuote, StartCR bool
	Prefix, CRValue  []byte
	FStart           int
}

func New(h Handler, maxFB, base int, in Init) *Lexer {
	l := &Lexer{h: h, maxVal: maxFB, base: base, rec: 1, fld: 1}
	if in.InQuote {
		l.state, l.quoted, l.recOpen, l.fStart = stQ, true, true, in.FStart
		l.val = append(l.val, in.Prefix...)
	}
	if in.StartCR {
		l.state, l.crField, l.recOpen, l.fStart = stCR, true, true, in.FStart
		l.val = append(l.val, in.CRValue...)
	}
	return l
}

func (l *Lexer) BytesProcessed() int64 { return l.bytes }

type EndKind int

const (
	EndBoundary  EndKind = iota // 记录边界
	EndUnquoted                 // 未引号字段中
	EndQuote                    // 引号字段中
	EndQuoteEnd                 // 引号刚闭合/逗号后悬挂
	EndCRPending                // CR 待定
)

type Snap struct {
	Kind    EndKind
	Partial cell.Cell
}

func (l *Lexer) Snapshot() Snap {
	s := Snap{Partial: cell.Cell{Value: string(l.val), Quoted: l.quoted, Start: l.fStart, End: l.base}}
	switch l.state {
	case stB:
		s.Kind = EndUnquoted
	case stQ:
		s.Kind = EndQuote
	case stQE:
		s.Kind = EndQuoteEnd
	case stCR:
		s.Kind = EndCRPending
	default:
		if l.recOpen {
			s.Kind = EndQuoteEnd
		}
	}
	return s
}

func (l *Lexer) fail(err error, off int) *PosError {
	if l.fatal == nil {
		l.fatal = &PosError{err, off, l.rec, l.fld}
	}
	return l.fatal
}

func (l *Lexer) addByte(off int, c byte) *PosError {
	if l.maxVal > 0 && len(l.val) >= l.maxVal {
		return l.fail(ErrFieldTooLong, off)
	}
	l.val = append(l.val, c)
	return nil
}

func (l *Lexer) emitField(end int) *PosError {
	c := cell.Cell{Value: string(l.val), Quoted: l.quoted, Start: l.fStart, End: end}
	l.val, l.quoted, l.recOpen, l.fld = l.val[:0], false, true, l.fld+1
	if err := l.h.Field(c); err != nil {
		l.fld--
		return l.fail(err, end)
	}
	return nil
}

func (l *Lexer) endRecord() *PosError {
	l.rec, l.fld, l.recOpen = l.rec+1, 1, false
	if err := l.h.EndRecord(); err != nil {
		return l.fail(err, l.base)
	}
	return nil
}
