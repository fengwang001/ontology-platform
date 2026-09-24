package lexer

import (
	"errors"
	"fmt"
	"ontology/cell"
)

const (
	KindBareQuote, KindQuoteAfterClose, KindUnterminated, KindOrphanCR = "bare quote", "quote after close", "unterminated quote", "orphan carriage return"
	KindFieldLimit, KindFieldsLimit, KindRecordsLimit, KindColumnCount = "field too large", "too many fields", "too many records", "column count mismatch"
)

type Limits struct{ MaxFieldBytes, MaxFields, MaxRecords int }
type Error struct {
	Kind          string
	Offset        int64
	Record, Field int
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s at byte %d record %d field %d", e.Kind, e.Offset, e.Record, e.Field)
}
func (e *Error) Is(t error) bool { x, ok := t.(*Error); return ok && x != nil && x.Kind == e.Kind }

var ErrBareQuote = errors.New(KindBareQuote)

type Emitter interface {
	Field(cell.Cell) error
	RecordEnd(offset int64) error
}
type st uint8

const (
	sStart st = iota
	sPlain
	sQuote
	sQClose
	sCR
	sQCR
)

type Boundary struct {
	State                 uint8
	Pos                   int64
	Rec                   int
	Field                 int
	Start                 int64
	Value                 []byte
	Quoted, Active, Comma bool
}

const (
	StateStart uint8 = iota
	StatePlain
	StateQuoted
	StateQuoteClose
	StateCR
	StateQuoteCR
)

func NewAt(out Emitter, lim Limits, b Boundary) *Machine {
	return &Machine{out, lim, b.Pos, b.Rec, b.Field, st(b.State), b.Start, append([]byte(nil), b.Value...), b.Quoted, b.Active, b.Comma, 0, nil}
}

type Machine struct {
	out                   Emitter
	lim                   Limits
	pos                   int64
	recNo, fieldNo        int
	st                    st
	start                 int64
	val                   []byte
	quoted, active, comma bool
	n                     int64
	fatal                 error
}

func New(out Emitter, lim Limits) *Machine { return &Machine{out: out, lim: lim, recNo: 1, fieldNo: 1} }
func (m *Machine) Feed(p []byte) error {
	if m.fatal != nil {
		return m.fatal
	}
	for _, b := range p {
		m.n++
		off := m.pos
		m.pos++
		if err := m.step(b, off); err != nil {
			m.fatal = err
			return err
		}
	}
	return nil
}
func (m *Machine) step(b byte, o int64) error {
	switch m.st {
	case sStart, sPlain:
		switch b {
		case '"':
			if m.st != sStart || m.active {
				return m.err(o, KindBareQuote)
			}
			m.st, m.quoted, m.active, m.start = sQuote, true, true, o
		case ',':
			if err := m.field(o + 1); err != nil {
				return err
			}
			m.comma = true
		case '\r':
			m.st = sCR
		case '\n':
			return m.row(o + 1)
		default:
			if !m.active {
				m.active, m.start = true, o
			}
			m.st = sPlain
			return m.add(b, o)
		}
	case sQuote:
		if b == '"' {
			m.st = sQClose
		} else if err := m.add(b, o); err != nil {
			return err
		}
	case sQClose:
		switch {
		case b == '"':
			m.st = sQuote
			return m.add('"', o)
		case b == ',':
			if err := m.field(o + 1); err != nil {
				return err
			}
			m.comma = true
		case b == '\r':
			m.st = sQCR
		case b == '\n':
			return m.row(o + 1)
		default:
			return m.err(o, KindQuoteAfterClose)
		}
	case sCR, sQCR:
		if b != '\n' {
			return m.err(o-1, KindOrphanCR)
		}
		return m.row(o + 1)
	}
	return nil
}
func (m *Machine) Close() error {
	if m.fatal != nil {
		return m.fatal
	}
	switch m.st {
	case sQuote:
		m.fatal = m.err(m.start, KindUnterminated)
	case sCR, sQCR:
		m.fatal = m.err(m.pos-1, KindOrphanCR)
	default:
		if m.active || m.comma {
			if err := m.field(m.pos); err != nil {
				m.fatal = err
			} else if err := m.out.RecordEnd(m.pos); err != nil {
				m.fatal = err
			}
		}
	}
	return m.fatal
}
func (m *Machine) add(b byte, o int64) error {
	if m.lim.MaxFieldBytes > 0 && len(m.val) >= m.lim.MaxFieldBytes {
		return m.err(o, KindFieldLimit)
	}
	m.val = append(m.val, b)
	return nil
}
func (m *Machine) field(e int64) error {
	err := m.out.Field(cell.Cell{string(m.val), m.quoted, m.start, e, m.recNo, m.fieldNo})
	m.val, m.quoted, m.active, m.st = nil, false, false, sStart
	m.fieldNo, m.start = m.fieldNo+1, e
	return err
}
func (m *Machine) row(e int64) error {
	if m.active || m.comma {
		if err := m.field(e); err != nil {
			return err
		}
		if err := m.out.RecordEnd(e); err != nil {
			return err
		}
	}
	m.recNo, m.fieldNo, m.comma, m.st, m.start = m.recNo+1, 1, false, sStart, e
	m.val, m.quoted, m.active = nil, false, false
	return nil
}
func (m *Machine) err(o int64, k string) error { return &Error{k, o, m.recNo, m.fieldNo} }
func (m *Machine) Bytes() int64                { return m.n }
func (m *Machine) Err() error                  { return m.fatal }
func (m *Machine) Snapshot() Boundary {
	return Boundary{uint8(m.st), m.pos, m.recNo, m.fieldNo, m.start, append([]byte(nil), m.val...), m.quoted, m.active, m.comma}
}

func (m *Machine) SegmentEnd() (Boundary, error) {
	b := m.Snapshot()
	if m.st == sQuote || m.st == sPlain {
		if err := m.field(m.pos); err != nil {
			return b, err
		}
	}
	return m.Snapshot(), nil
}
