// Package lexer 是可暂停续传的 RFC4180 方言 CSV 状态机；流式与 par 分段共用一台机器。
package lexer

import (
	"errors"
	"fmt"
	"strings"

	"ontology/cell"
)

type State int

const (
	StateStart State = iota
	StateBare
	StateQuote
	StateQQuote
	StateCR
)

type Kind int

const (
	KindBareQuote Kind = iota
	KindQuoteClosed
	KindUnterminated
	KindLoneCR
	KindFieldTooLarge
	KindTooManyFields
	KindTooManyRecords
	KindColumnMismatch
)

var (
	ErrBareQuote      = errors.New("csv: bare quote in unquoted field")
	ErrQuoteClosed    = errors.New("csv: unexpected char after closing quote")
	ErrUnterminated   = errors.New("csv: unterminated quoted field")
	ErrLoneCR         = errors.New("csv: bare carriage return")
	ErrFieldTooLarge  = errors.New("csv: field too large")
	ErrTooManyFields  = errors.New("csv: too many fields in record")
	ErrTooManyRecords = errors.New("csv: too many records")
	ErrColumnMismatch = errors.New("csv: field count mismatch")
)

var sentinels = [...]error{ErrBareQuote, ErrQuoteClosed, ErrUnterminated, ErrLoneCR,
	ErrFieldTooLarge, ErrTooManyFields, ErrTooManyRecords, ErrColumnMismatch}

type Error struct {
	Kind                  Kind
	Offset, Record, Field int
} // 偏移从0起,编号从1起

func (e *Error) Error() string {
	return fmt.Sprintf("%s at byte %d (record %d, field %d)", sentinels[e.Kind], e.Offset, e.Record, e.Field)
}
func (e *Error) Unwrap() error { return sentinels[e.Kind] }

type Limits struct{ MaxFieldBytes, MaxFieldsPerRecord, MaxRecords int } // 0=不限

type Event struct {
	Cell          cell.Cell
	Record, Field int
	End           bool
	Offset        int
}

type Sink interface{ Send(Event) }

// Machine 可指定初始状态。steps 是非导出计数器：每喂入一字节恰好 +1，不回扫。
type Machine struct {
	st, base, off, vlen, cs, rec, fld, oq, cr, ov int
	qt, seen, rep, head                           bool
	lim                                           Limits
	sink                                          Sink
	val                                           strings.Builder
	err                                           *Error
	steps                                         int64
}

func NewMachine(st0 State, base, rec, fld int, head bool, lim Limits, s Sink) *Machine {
	m := &Machine{st: int(st0), base: base, rec: rec, fld: fld, head: head, lim: lim, sink: s}
	switch st0 {
	case StateCR:
		m.rep, m.cr = true, base-1
	case StateQuote:
		m.seen, m.qt, m.oq, m.cs = true, true, base-1, base-1
	}
	return m
}

func (m *Machine) fail(k Kind, off int) *Error {
	if m.err == nil {
		if m.fld == 0 {
			m.err = &Error{k, off, m.rec, 1}
		} else {
			m.err = &Error{k, off, m.rec, m.fld}
		}
	}
	return m.err
}

func (m *Machine) Err() *Error      { return m.err }
func (m *Machine) Steps() int64     { return m.steps }
func (m *Machine) State() State     { return State(m.st) }
func (m *Machine) RecCount() int    { return m.rec - 1 }
func (m *Machine) FieldNo() int     { return m.fld }
func (m *Machine) HeadFirst() bool  { return m.head && m.fld == 0 }
func (m *Machine) SetOverlap(n int) { m.ov = n }

func (m *Machine) grow(b byte, off int) bool {
	m.val.WriteByte(b)
	m.vlen++
	if m.lim.MaxFieldBytes > 0 && m.vlen > m.lim.MaxFieldBytes {
		m.fail(KindFieldTooLarge, off)
		return false
	}
	return true
}

func (m *Machine) emit(off int) bool {
	if m.lim.MaxFieldsPerRecord > 0 && m.fld+1 > m.lim.MaxFieldsPerRecord {
		m.fail(KindTooManyFields, m.cs)
		return false
	}
	m.fld++
	v := m.val.String()
	if m.ov > 0 && m.fld == 1 {
		v = v[m.ov:]
	}
	end := off
	if m.qt {
		end = off + 1
	}
	if m.rep && !m.qt {
		end = m.cr
	}
	m.sink.Send(Event{Cell: cell.Cell{Value: v, Quoted: m.qt, Start: m.cs, End: end}, Record: m.rec, Field: m.fld})
	m.val.Reset()
	m.vlen, m.qt, m.seen = 0, false, true
	return true
}

func (m *Machine) endRec(off int) bool {
	if m.lim.MaxRecords > 0 && m.rec > m.lim.MaxRecords {
		m.fail(KindTooManyRecords, off)
		return false
	}
	m.sink.Send(Event{End: true, Record: m.rec, Field: m.fld, Offset: off})
	m.rec, m.fld, m.seen = m.rec+1, 0, false
	return true
}

func (m *Machine) onStart(b byte, off int) bool {
	switch b {
	case ',':
		m.cs = off
		return m.emit(off)
	case '\n':
		return m.endRec(off)
	case '\r':
		m.cr, m.st = off, int(StateCR)
	case '"':
		return m.fail(KindBareQuote, off) == nil
	default:
		m.cs = off
		if m.grow(b, off) {
			m.st = int(StateBare)
		} else {
			return false
		}
	}
	return true
}

func (m *Machine) onBare(b byte, off int) bool {
	switch b {
	case ',':
		if m.emit(off) {
			m.st = int(StateStart)
		} else {
			return false
		}
	case '\n':
		return m.emit(off) && m.endRec(off)
	case '\r':
		m.cr, m.st = off, int(StateCR)
	case '"':
		return m.fail(KindBareQuote, off) == nil
	default:
		return m.grow(b, off)
	}
	return true
}

func (m *Machine) onCR(b byte, off int) bool {
	switch b {
	case '\n':
		if m.seen {
			m.emit(m.cr)
		}
		if m.endRec(off) {
			m.rep, m.st = false, int(StateStart)
		} else {
			return false
		}
	case '"':
		return m.fail(KindBareQuote, off) == nil
	default:
		return m.fail(KindLoneCR, m.cr) == nil
	}
	return true
}

func (m *Machine) onQQ(b byte, off int) bool {
	switch b {
	case ',':
		m.emit(off)
		m.st = int(StateStart)
	case '\n':
		m.emit(off)
		return m.endRec(off)
	case '\r':
		m.cr, m.st = off, int(StateCR)
	case '"':
		if m.grow('"', off) {
			m.st = int(StateQuote)
		} else {
			return false
		}
	default:
		return m.fail(KindQuoteClosed, off) == nil
	}
	return true
}

func (m *Machine) Run(p []byte) *Error {
	for _, b := range p {
		if m.err != nil {
			return m.err
		}
		off := m.base + m.off
		m.off, m.steps = m.off+1, m.steps+1
		var ok bool
		switch State(m.st) {
		case StateStart:
			ok = m.onStart(b, off)
		case StateBare:
			ok = m.onBare(b, off)
		case StateCR:
			ok = m.onCR(b, off)
		case StateQuote:
			if b == '"' {
				m.st, ok = int(StateQQuote), true
			} else {
				ok = m.grow(b, off)
			}
		case StateQQuote:
			ok = m.onQQ(b, off)
		}
		if !ok {
			return m.err
		}
	}
	return m.err
}

func (m *Machine) Close() *Error { // 仅末段调用
	if m.err == nil {
		switch State(m.st) {
		case StateQuote, StateQQuote:
			m.fail(KindUnterminated, m.oq)
		case StateCR:
			m.fail(KindLoneCR, m.cr)
		default:
			if m.seen {
				m.emit(m.base + m.off)
				m.endRec(m.base + m.off)
			}
		}
	}
	return m.err
}

type Lexer struct{ m *Machine } // 单实例非并发安全

func New(lim Limits, s Sink) *Lexer { return &Lexer{NewMachine(StateStart, 0, 1, 0, false, lim, s)} }
func (l *Lexer) Feed(p []byte) error {
	if e := l.m.Run(p); e != nil {
		return e
	}
	l.m.base += len(p)
	return nil
}
func (l *Lexer) Close() error { return l.m.Close() }
func (l *Lexer) Steps() int64 { return l.m.Steps() }
