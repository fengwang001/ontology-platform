package lexer

import (
	"errors"
	"ontology/cell"
)

var (
	ErrQuoteInBare    = errors.New("quote in unquoted field")
	ErrQuoteClosed    = errors.New("character after closed quote")
	ErrUnclosedQuote  = errors.New("unclosed quoted field")
	ErrDanglingCR     = errors.New("carriage return not followed by newline")
	ErrFieldTooLarge  = errors.New("field exceeds byte limit")
	ErrTooManyFields  = errors.New("record exceeds field limit")
	ErrTooManyRecords = errors.New("input exceeds record limit")
)

type Error struct {
	Err                   error
	Offset, Record, Field int
}

func (e *Error) Error() string { return e.Err.Error() }
func (e *Error) Unwrap() error { return e.Err }

type Limits struct{ MaxFieldBytes, MaxFields, MaxRecords int }
type Sink interface {
	Field(cell.Cell)
	EndRecord()
}

const (
	StartFresh = iota
	StartBare
	StartQuoted
	StartClosed
	StartCR
)

type State struct {
	Mode                              int
	Cur                               cell.Cell
	Open                              bool
	Pos, Record, Field, CR, Processed int
}
type Result struct {
	Rows  [][]cell.Cell
	State State
	Err   *Error
}

type machine struct {
	lim  Limits
	st   State
	rows [][]cell.Cell
}

func newMachine(lim Limits, pos int) *machine {
	return &machine{lim: lim, st: State{Record: 1, Field: 1, Pos: pos, Cur: cell.Cell{Start: pos, End: pos}}}
}
func (m *machine) fail(e error, pos int) *Error { return &Error{e, pos, m.st.Record, m.st.Field} }
func (m *machine) put() {
	if len(m.rows) < m.st.Record {
		m.rows = append(m.rows, nil)
	}
	m.rows[m.st.Record-1] = append(m.rows[m.st.Record-1], m.st.Cur)
}
func (m *machine) add(s string, pos int) *Error {
	m.st.Cur.Value += s
	if m.lim.MaxFieldBytes > 0 && len(m.st.Cur.Value) > m.lim.MaxFieldBytes {
		return m.fail(ErrFieldTooLarge, pos)
	}
	return nil
}
func (m *machine) next(pos int) *Error {
	if m.lim.MaxFields > 0 && m.st.Field >= m.lim.MaxFields {
		return m.fail(ErrTooManyFields, pos)
	}
	m.put()
	m.st.Field++
	m.st.Cur = cell.Cell{Start: pos, End: pos}
	m.st.Open = true
	m.st.Mode = StartFresh
	return nil
}
func (m *machine) line(pos int) {
	m.put()
	m.st.Record++
	m.st.Field = 1
	m.st.Cur = cell.Cell{Start: pos, End: pos}
	m.st.Open = false
	m.st.Mode = StartFresh
}

func (m *machine) feed(p []byte) *Error {
	for i, b := range p {
		pos := m.st.Pos + i
		switch m.st.Mode {
		case StartFresh, StartBare:
			switch {
			case b == ',':
				m.st.Cur.End = pos
				if e := m.next(pos + 1); e != nil {
					return e
				}
			case b == '"':
				if m.st.Mode == StartBare {
					return m.fail(ErrQuoteInBare, pos)
				}
				m.st.Cur.Quoted = true
				m.st.Open = true
				m.st.Mode = StartQuoted
			case b == '\r':
				m.st.Cur.End = pos
				m.st.Mode = StartCR
				m.st.CR = pos
			case b == '\n':
				if m.st.Open {
					m.st.Cur.End = pos
					m.line(pos + 1)
				} else {
					m.st.Cur = cell.Cell{Start: pos + 1, End: pos + 1}
				}
			default:
				if e := m.add(string(b), pos); e != nil {
					return e
				}
				m.st.Open = true
				m.st.Mode = StartBare
			}
		case StartQuoted:
			if b == '"' {
				m.st.Cur.End = pos + 1
				m.st.Mode = StartClosed
			} else if e := m.add(string(b), pos); e != nil {
				return e
			}
		case StartClosed:
			switch b {
			case '"':
				if e := m.add("\"", pos); e != nil {
					return e
				}
				m.st.Mode = StartQuoted
			case ',':
				if e := m.next(pos + 1); e != nil {
					return e
				}
			case '\r':
				m.st.Mode = StartCR
				m.st.CR = pos
			case '\n':
				m.line(pos + 1)
			default:
				return m.fail(ErrQuoteClosed, pos)
			}
		case StartCR:
			if b != '\n' {
				return m.fail(ErrDanglingCR, m.st.CR)
			}
			m.line(pos + 1)
		}
	}
	m.st.Pos += len(p)
	m.st.Processed += len(p)
	return nil
}

func (m *machine) finish() *Error {
	switch m.st.Mode {
	case StartQuoted:
		return m.fail(ErrUnclosedQuote, m.st.Cur.Start)
	case StartCR:
		return m.fail(ErrDanglingCR, m.st.CR)
	}
	if m.st.Open {
		if m.st.Mode != StartClosed {
			m.st.Cur.End = m.st.Pos
		}
		m.put()
	}
	return nil
}

type Lexer struct {
	m *machine
	s Sink
	e *Error
}

func New(s Sink, lim Limits) *Lexer { return &Lexer{m: newMachine(lim, 0), s: s} }
func (l *Lexer) Feed(p []byte) error {
	if l.e != nil {
		return l.e
	}
	if e := l.m.feed(p); e != nil {
		l.e = e
		return e
	}
	emit(l.s, l.m.rows)
	l.m.rows = nil
	return nil
}
func (l *Lexer) Close() error {
	if l.e != nil {
		return l.e
	}
	if e := l.m.finish(); e != nil {
		l.e = e
		return e
	}
	emit(l.s, l.m.rows)
	return nil
}
func (l *Lexer) Processed() int { return l.m.st.Processed }

func emit(s Sink, rows [][]cell.Cell) {
	for _, r := range rows {
		for _, c := range r {
			s.Field(c)
		}
		s.EndRecord()
	}
}

func Segment(p []byte, start int, lim Limits, init State, final bool) Result {
	m := &machine{lim: lim, st: init}
	if init.Record == 0 {
		m = newMachine(lim, start)
	}
	if start > 0 {
		m.st.Pos = start
	}
	e := m.feed(p)
	if e == nil && final {
		e = m.finish()
	}
	return Result{m.rows, m.st, e}
}
