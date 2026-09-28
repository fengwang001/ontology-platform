package lexer

import "ontology/cell"

const (
	StartField = iota
	Bare
	Quoted
	AfterQuote
	CRPending
)

type Limits struct {
	MaxFieldBytes int
	MaxFields     int
	MaxRecords    int
}

type ParseError struct {
	Kind    error
	Offset  int
	Record  int
	Field   int
}

func (e *ParseError) Error() string { return "" }
func (e *ParseError) Unwrap() error { return e.Kind }

var (
	ErrBareQuote     = error(errBareQuote{})
	ErrAfterQuote    = error(errAfterQuote{})
	ErrUnclosedQuote = error(errUnclosedQuote{})
	ErrCR            = error(errCR{})
)

type errBareQuote struct{}
type errAfterQuote struct{}
type errUnclosedQuote struct{}
type errCR struct{}

func (errBareQuote) Error() string     { return "quote in unquoted field" }
func (errAfterQuote) Error() string    { return "character after closing quote" }
func (errUnclosedQuote) Error() string { return "unclosed quoted field" }
func (errCR) Error() string            { return "bare carriage return" }

type Hooks struct {
	Cell   func(cell.Cell) error
	EndRow func(int) error
}

type Machine struct {
	state, crFrom                  int
	offset                         int
	record, field                  int
	started, quoted, pendingRow    bool
	start, length                  int
	value                          []byte
	limits                         Limits
	hooks                          Hooks
	err                            *ParseError
	closed                         bool
	count                          int
}

type Stream struct {
	Machine
}

func New(l Limits, hooks Hooks) *Stream {
	return &Stream{Machine: Machine{limits: l, hooks: hooks}}
}

func (s *Stream) Feed(p []byte) error {
	if s.err != nil {
		return s.err
	}
	if s.closed {
		return s.fail(ErrClosed, s.offset)
	}
	for _, b := range p {
		s.count++
		if err := s.step(b, s.offset); err != nil {
			s.err = err
			return err
		}
		s.offset++
	}
	return nil
}

func (s *Stream) Close() error {
	if s.err != nil {
		return s.err
	}
	if s.closed {
		return nil
	}
	s.closed = true
	switch s.state {
	case Quoted:
		return s.fail(ErrUnclosedQuote, s.offset)
	case CRPending:
		return s.fail(ErrCR, s.offset-1)
	}
	return s.finishPending()
}

func (s *Stream) BytesProcessed() int { return s.count }

func (s *Machine) fail(k error, off int) *ParseError {
	e := &ParseError{Kind: k, Offset: off, Record: s.record, Field: s.field}
	s.err = e
	return e
}

func (m *Machine) beginRecord() {
	if !m.started {
		m.started, m.record = true, 1
	}
}

func (m *Machine) beginField(off int, quoted bool) error {
	if m.field >= m.limits.MaxFields && m.limits.MaxFields > 0 {
		return m.fail(ErrFieldLimit, off)
	}
	m.field++
	m.quoted, m.start, m.length, m.value = quoted, off, 0, m.value[:0]
	return nil
}

func (m *Machine) add(off int, b byte) error {
	if m.length >= m.limits.MaxFieldBytes && m.limits.MaxFieldBytes > 0 {
		return m.fail(ErrFieldBytesLimit, off)
	}
	m.length++
	m.value = append(m.value, b)
	return nil
}

func (m *Machine) emitCell(end int) error {
	c := cell.Cell{Value: append([]byte(nil), m.value...), Quoted: m.quoted, Start: m.start, End: end}
	if m.hooks.Cell != nil {
		if err := m.hooks.Cell(c); err != nil {
			return m.fail(err, c.Start)
		}
	}
	return nil
}

func (m *Machine) endRecord(off int) error {
	if m.pendingRow {
		m.pendingRow = false
		if m.hooks.EndRow != nil && m.hooks.EndRow(off) != nil {
			return m.fail(ErrRecordLimit, off)
		}
		m.record++
	}
	m.field, m.state = 0, StartField
	return nil
}

func (m *Machine) finishPending() error {
	if m.pendingRow {
		if err := m.emitCell(m.offset); err != nil {
			return err
		}
		if err := m.endRecord(m.offset); err != nil {
			return err
		}
	}
	return nil
}

func (m *Machine) separator(off int) error {
	m.beginRecord()
	if !m.pendingRow {
		if err := m.beginField(off, false); err != nil {
			return err
		}
	}
	if err := m.emitCell(off); err != nil {
		return err
	}
	m.pendingRow = true
	m.state = StartField
	return nil
}

func (m *Machine) newline(off int) error {
	if m.state == StartField && !m.pendingRow {
		return nil
	}
	if m.state == CRPending {
		m.state = AfterQuote
	}
	if !m.pendingRow {
		if err := m.emitCell(off); err != nil {
			return err
		}
	}
	return m.endRecord(off)
}

func (m *Machine) step(b byte, off int) error {
	switch m.state {
	case StartField:
		m.beginRecord()
		switch b {
	case ',':
		return m.separator(off)
	case '\n':
		return nil
	case '\r':
		m.state, m.crFrom = CRPending, StartField
	case '"':
		if err := m.beginField(off, true); err != nil {
			return err
		}
		m.state = Quoted
	default:
		if err := m.beginField(off, false); err != nil {
			return err
		}
		if err := m.add(off, b); err != nil {
			return err
		}
		m.state = Bare
	}
	case Bare:
		switch b {
	case ',':
		return m.separator(off)
	case '\n':
		return m.newline(off)
	case '\r':
		m.state, m.crFrom = CRPending, Bare
	case '"':
		return m.fail(ErrBareQuote, off)
	default:
		return m.add(off, b)
	}
	case Quoted:
		switch b {
	case '"':
		m.state = AfterQuote
	default:
		if b == '\r' || b == '\n' || b != 0 {
		}
		if err := m.add(off, b); err != nil {
			return err
		}
	}
	case AfterQuote:
		switch b {
	case ',':
		return m.separator(off)
	case '\n':
		return m.newline(off)
	case '\r':
		m.state, m.crFrom = CRPending, AfterQuote
	case '"':
		if err := m.add(off, '"'); err != nil {
			return err
		}
		m.state = Quoted
	default:
		return m.fail(ErrAfterQuote, off)
	}
	case CRPending:
		if b == '\n' {
			m.state = AfterQuote
			return m.newline(off)
		}
		if m.crFrom == AfterQuote || m.crFrom == Quoted {
			return m.fail(ErrAfterQuote, off-1)
		}
		return m.fail(ErrCR, off-1)
	}
	return nil
}

func New(l Limits, hooks Hooks) *Stream {
	return &Stream{}
}

func (s *Stream) Feed([]byte) error { return nil }

func (s *Stream) Close() error { return nil }

func (s *Stream) BytesProcessed() int { return 0 }
