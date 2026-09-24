package lexer

import "errors"

var (
	ErrBareQuote     = errors.New("bare double quote")
	ErrAfterQuote    = errors.New("character after closing quote")
	ErrUnclosedQuote = errors.New("unclosed quoted field")
	ErrDanglingCR    = errors.New("carriage return not followed by newline")
)

type State int

const (
	Start State = iota
	Unquoted
	Quoted
	QuoteSeen
	CRRecord
	CRBlank
)

type Handler interface {
	FieldStart(off int, quoted bool)
	Data(off int, b byte)
	FieldEnd(off int)
	RecordEnd(off int)
	SkipBlank(off int)
}

type Machine struct {
	state         State
	recordStarted bool
	fieldOpen     bool
	quoteStart    int
	crPos         int
	pos           int
	count         int64
	closed        bool
	fatal         *Error
}

func New() *Machine { return &Machine{} }

func NewAt(state State, recordStarted, fieldOpen bool, quoteStart int) *Machine {
	return &Machine{state: state, recordStarted: recordStarted, fieldOpen: fieldOpen, quoteStart: quoteStart}
}

func (m *Machine) Count() int64 { return m.count }
func (m *Machine) State() State { return m.state }
func (m *Machine) Pos() int     { return m.pos }
func (m *Machine) RecordStarted() bool { return m.recordStarted }
func (m *Machine) FieldOpen() bool    { return m.fieldOpen }
func (m *Machine) QuoteStart() int    { return m.quoteStart }

func (m *Machine) Feed(p []byte, h Handler) error {
	return m.FeedAt(p, m.pos, h)
}

type Error struct {
	Kind   error
	Offset int
}

func (e *Error) Error() string { return e.Kind.Error() }
func (e *Error) Unwrap() error { return e.Kind }

func (m *Machine) fail(kind error, off int) error {
	m.state = Start
	m.closed = true
	m.fatal = &Error{Kind: kind, Offset: off}
	return m.fatal
}

func (m *Machine) FeedAt(p []byte, base int, h Handler) error {
	if m.fatal != nil {
		return m.fatal
	}
	for i, b := range p {
		off := base + i
		m.count++
		switch m.state {
		case Start:
			switch b {
			case ',':
				if !m.recordStarted {
					m.recordStarted = true
				}
				h.FieldStart(off, false)
				h.FieldEnd(off)
			case '\n':
				if m.recordStarted {
					h.FieldEnd(off)
					h.RecordEnd(off)
					m.recordStarted = false
				} else {
					h.SkipBlank(off)
				}
			case '\r':
				m.state = CRBlank
				m.crPos = off
			case '"':
				if !m.recordStarted {
					m.recordStarted = true
				}
				m.state = Quoted
				m.fieldOpen = true
				m.quoteStart = off
				h.FieldStart(off, true)
			default:
				if !m.recordStarted {
					m.recordStarted = true
				}
				m.state = Unquoted
				m.fieldOpen = true
				h.FieldStart(off, false)
				h.Data(off, b)
			}
		case Unquoted:
			switch b {
			case ',':
				h.FieldEnd(off)
				h.FieldStart(off, false)
				m.state = Start
			case '\n':
				h.FieldEnd(off)
				h.RecordEnd(off)
				m.recordStarted = false
				m.fieldOpen = false
				m.state = Start
			case '\r':
				m.state = CRRecord
				m.crPos = off
			case '"':
				return m.fail(ErrBareQuote, off)
			default:
				h.Data(off, b)
			}
		case Quoted:
			switch b {
			case '"':
				m.state = QuoteSeen
			default:
				h.Data(off, b)
			}
		case QuoteSeen:
			switch b {
			case '"':
				h.Data(off, '"')
				m.state = Quoted
			case ',':
				h.FieldEnd(off)
				h.FieldStart(off, false)
				m.state = Start
			case '\n':
				h.FieldEnd(off)
				h.RecordEnd(off)
				m.recordStarted = false
				m.fieldOpen = false
				m.state = Start
			case '\r':
				m.state = CRRecord
				m.crPos = off
			default:
				return m.fail(ErrAfterQuote, m.quoteStart)
			}
		case CRRecord, CRBlank:
			if b != '\n' {
				return m.fail(ErrDanglingCR, m.crPos)
			}
			if m.state == CRRecord {
				h.FieldEnd(off)
				h.RecordEnd(off)
			} else {
				h.SkipBlank(off)
			}
			m.recordStarted = false
			m.fieldOpen = false
			m.state = Start
		}
	}
	m.pos = base + len(p)
	return nil
}

func (m *Machine) Close(h Handler) error {
	if m.fatal != nil {
		return m.fatal
	}
	m.closed = true
	switch m.state {
	case Quoted:
		if m.state == Quoted {
			m.fatal = &Error{Kind: ErrUnclosedQuote, Offset: m.quoteStart}
			return m.fatal
		}
	case CRRecord, CRBlank:
		m.fatal = &Error{Kind: ErrDanglingCR, Offset: m.crPos}
		return m.fatal
	}
	if m.recordStarted {
		if m.fieldOpen {
			h.FieldEnd(m.pos)
		}
		h.RecordEnd(m.pos)
	}
	return nil
}
