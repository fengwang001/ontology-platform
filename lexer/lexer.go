package lexer

import (
	"errors"
	"fmt"
	"ontology/cell"
	"strings"
)

var ErrBadQuote = errors.New("bare '\"' in unquoted field")
var ErrQuoteAfterClose = errors.New("unexpected byte after closing quote")
var ErrUnclosedQuote = errors.New("unterminated quoted field")
var ErrBareCR = errors.New("lone '\\r' not followed by '\\n'")
var ErrFieldTooLarge = errors.New("field exceeds byte limit")
var ErrTooManyFields = errors.New("record exceeds field count limit")

type Limits struct{ MaxFieldBytes, MaxFields int } // 0 不限；记录数上限在 table 包
type PosError struct {
	Err             error
	Off, Rec, Field int
}

func (e *PosError) Error() string {
	return fmt.Sprintf("%s (offset %d, record %d, field %d)", e.Err, e.Off, e.Rec, e.Field)
}
func (e *PosError) Unwrap() error { return e.Err }

type Sink interface {
	Cell(c cell.Cell, rec, field int)
	Row(rec, end int)
}

const StR, StF, StU, StQ, StQQ, StCR = 0, 1, 2, 3, 4, 5

type M struct {
	Sink                                 Sink
	MaxBytes, MaxFields                  int
	St                                   int
	Active, Quoted, CRUn                 bool
	Val                                  strings.Builder
	Fs, FLen, CROff, NRec, NField, Reads int
}

func NewM(s Sink, lim Limits) *M {
	return &M{Sink: s, MaxBytes: lim.MaxFieldBytes, MaxFields: lim.MaxFields, NRec: 1}
}
func (m *M) fail(e error, o int) *PosError { return &PosError{e, o, m.NRec, m.NField} }
func (m *M) act(o int)                     { m.Active, m.NField, m.Fs = true, 1, o }
func (m *M) reset()                        { m.Val.Reset(); m.FLen, m.Quoted = 0, false }
func (m *M) send(o int)                    { m.Sink.Cell(cell.New(m.Val.String(), m.Quoted, m.Fs, o), m.NRec, m.NField) }
func (m *M) emit(o int)                    { m.send(o); m.reset() }
func (m *M) row(e int) {
	m.Sink.Row(m.NRec, e)
	m.NRec, m.Active, m.NField, m.St = m.NRec+1, false, 0, StR
}
func (m *M) content(b byte, o int) error {
	if m.FLen++; m.MaxBytes > 0 && m.FLen > m.MaxBytes {
		return m.fail(ErrFieldTooLarge, o)
	}
	m.Val.WriteByte(b)
	return nil
}
func (m *M) comma(o int) error {
	m.emit(o)
	if m.NField++; m.MaxFields > 0 && m.NField > m.MaxFields {
		return m.fail(ErrTooManyFields, o)
	}
	m.Fs = o + 1
	return nil
}

// Step 处理绝对偏移 o 处的一个字节；每字节恰好调用一次，不回扫。
func (m *M) Step(b byte, o int) error {
	switch m.St {
	case StCR:
		if b != '\n' {
			return m.fail(ErrBareCR, m.CROff)
		}
		if m.Active {
			if m.CRUn {
				m.emit(m.CROff)
			}
			m.row(o + 1)
		}
		m.St = StR
	case StQ:
		if b == '"' {
			m.St = StQQ
		} else {
			return m.content(b, o)
		}
	case StQQ:
		switch b {
		case '"':
			m.St = StQ
			return m.content('"', o)
		case ',':
			return m.comma(o)
		case '\n':
			m.emit(o)
			m.row(o + 1)
		case '\r':
			m.emit(o)
			m.St, m.CROff, m.CRUn = StCR, o, false
		default:
			return m.fail(ErrQuoteAfterClose, o)
		}
	default:
		switch b {
		case ',':
			if m.St == StR {
				m.act(o)
			}
			return m.comma(o)
		case '\n':
			if m.Active {
				m.emit(o)
				m.row(o + 1)
			}
			m.St = StR
		case '\r':
			m.St, m.CROff, m.CRUn = StCR, o, true
		case '"':
			if m.St == StU {
				return m.fail(ErrBadQuote, o)
			}
			if m.St == StR {
				m.act(o)
			}
			m.Quoted, m.St = true, StQ
		default:
			if m.St == StR {
				m.act(o)
			}
			m.St = StU
			return m.content(b, o)
		}
	}
	return nil
}

// CloseAt 复刻流结束语义（提交末记录或报未闭合/孤立 CR）。
func (m *M) CloseAt(end int) error {
	switch m.St {
	case StR:
		return nil
	case StCR:
		return m.fail(ErrBareCR, m.CROff)
	case StQ:
		return m.fail(ErrUnclosedQuote, end)
	}
	m.emit(end)
	m.row(end)
	return nil
}
