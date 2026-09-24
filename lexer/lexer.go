// Package lexer 是可暂停续传的逐字节 CSV 状态机。
package lexer

import (
	"errors"
	"fmt"

	"ontology/cell"
)

// 可判定哨兵错误。
var (
	ErrBareQuote     = errors.New("bare quote in unquoted field")
	ErrQuoteClose    = errors.New("unexpected char after closing quote")
	ErrUnclosedQuote = errors.New("unterminated quoted field")
	ErrDanglingCR    = errors.New("bare carriage return")
	ErrFieldTooLong  = errors.New("field exceeds max bytes")
	ErrTooManyFields = errors.New("record exceeds max fields")
	ErrTooManyRecords = errors.New("too many records")
)

// PosError 携带字节偏移（0 起）、记录号、字段号（1 起）。
type PosError struct {
	Err    error
	Byte   int
	Record int
	Field  int
}

func (e *PosError) Error() string {
	return fmt.Sprintf("%v at byte %d (record %d field %d)", e.Err, e.Byte, e.Record, e.Field)
}
func (e *PosError) Unwrap() error { return e.Err }

// Is 支持 errors.Is(*PosError, sentinel)。
func (e *PosError) Is(target error) bool { return e.Err == target }

// Limits 是可配置上限；0 表示不限。
type Limits struct {
	MaxFieldBytes  int
	MaxFields      int
	MaxRecords     int
}

const (
	stStart = iota
	stInF
	stInQ
	stQSeen
	stCR
)

// machine 是无缓冲的纯状态机，流式与并行两条路径共用，保证逐位一致。
type machine struct {
	st        uint8
	rec       int
	fld       int
	open      bool
	recStart  int
	fStart    int
	qStart    int
	size      int
	buf       []byte
	base      int
	leading   bool
	err       error
	lim       Limits
	processed int
}

func (m *machine) errAt(e error, off int) {
	if m.err == nil {
		m.err = &PosError{Err: e, Byte: m.base + off, Record: m.rec, Field: m.fld}
	}
}

func (m *machine) grow() {
	if m.lim.MaxFieldBytes > 0 && m.size >= m.lim.MaxFieldBytes {
		return false
	}
	m.size++
	return true
}

func (m *machine) emit(off int) {
	c := cell.Cell{Value: string(m.buf), Quoted: m.qStart >= 0, Start: m.recStart + m.fStart, End: m.base + off}
	if c.Quoted {
		c.Start = m.base + m.qStart
	}
	m.out = append(m.out, c)
	m.buf, m.size, m.qStart = m.buf[:0], 0, -1
}

func (m *machine) newField(off int) {
	m.fld++
	if m.lim.MaxFields > 0 && m.fld > m.lim.MaxFields {
		m.errAt(ErrTooManyFields, off)
	}
	m.fStart = off
}

func (m *machine) closeRec(off int) {
	if m.open {
		m.emit(off)
		m.rec++
		if m.lim.MaxRecords > 0 && m.rec > m.lim.MaxRecords {
			m.errAt(ErrTooManyRecords, off)
		}
	}
	m.open, m.fld, m.recStart, m.fStart = false, 0, off+1, 0
}

func (m *machine) step(b byte, i int) bool {
	switch m.st {
	case stStart:
		m.open = true
		switch b {
		case ',':
			m.emit(i)
			m.newField(i + 1)
		case '"':
			m.st, m.qStart = stInQ, i
		case '\r':
			m.st = stCR
		case '\n':
			m.closeRec(i)
		default:
			m.st = stInF
			if !m.grow() {
				m.errAt(ErrFieldTooLong, i)
				return false
			}
			m.buf = append(m.buf, b)
		}
	case stInF:
		switch b {
		case ',':
			m.emit(i)
			m.st = stStart
			m.newField(i + 1)
		case '"':
			m.errAt(ErrBareQuote, i)
			return false
		case '\r':
			m.st = stCR
		case '\n':
			m.closeRec(i)
			m.st = stStart
		default:
			if !m.grow() {
				m.errAt(ErrFieldTooLong, i)
				return false
			}
			m.buf = append(m.buf, b)
		}
	case stInQ:
		if b == '"' {
			m.st = stQSeen
			return true
		}
		if !m.grow() {
			m.errAt(ErrFieldTooLong, i)
			return false
		}
		m.buf = append(m.buf, b)
	case stQSeen:
		switch b {
		case '"':
			m.st = stInQ
			if !m.grow() {
				m.errAt(ErrFieldTooLong, i)
				return false
			}
			m.buf = append(m.buf, '"')
		case ',':
			m.emit(i)
			m.newField(i + 1)
			m.st = stStart
		case '\r':
			m.st = stCR
		case '\n':
			m.closeRec(i)
			m.st = stStart
		default:
			m.errAt(ErrQuoteClose, i)
			return false
		}
	case stCR:
		if b != '\n' {
			m.errAt(ErrDanglingCR, i-1)
			return false
		}
		m.closeRec(i - 1)
		m.st = stStart
	}
	return true
}
