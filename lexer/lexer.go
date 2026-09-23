package lexer

import (
	"errors"
	"strings"

	"ontology/cell"
)

const (
	Field = iota
	Record
	Blank
)

const (
	startField = iota
	unquoted
	quoted
	afterQuote
	crUnquoted
	crQuoted
)

var (
	ErrQuote         = errors.New("unexpected quote in unquoted field")
	ErrAfterQuote    = errors.New("unexpected character after quoted field")
	ErrUnterminated  = errors.New("unterminated quoted field")
	ErrLoneCR        = errors.New("bare carriage return")
	ErrFieldTooLarge = errors.New("field exceeds maximum bytes")
	ErrTooManyFields = errors.New("record exceeds maximum field count")
)

type Limits struct{ MaxFieldBytes, MaxFields int }

type PosError struct {
	Err    error
	Offset int
	Record int
	Field  int
}

func (e *PosError) Error() string { return e.Err.Error() }
func (e *PosError) Unwrap() error { return e.Err }

type Event struct {
	Kind        int
	Cell        cell.Cell
	Data        string
	StartField  bool
	StartLength int
	RecordDelta int
}

type State struct {
	Mode, Fields                  int
	InField, Quoted               bool
	Start, Length, Pending, Bytes int
}

type Scanner struct {
	limits Limits
	count  int
}

func NewScanner(maxField, maxFields int) *Scanner {
	return &Scanner{limits: Limits{maxField, maxFields}}
}

func (s *Scanner) Bytes() int { return s.count }

func (s *Scanner) Run(p []byte, base int, st State, final bool, emit func(Event) error) (State, error) {
	var b strings.Builder
	fail := func(err error, off int) (State, error) { return st, &PosError{err, off, 0, st.Fields + 1} }
	for i := 0; i < len(p); i++ {
		s.count++
		c := p[i]
		off := base + i
		add := func() error {
			if st.Length >= s.limits.MaxFieldBytes {
				return &PosError{ErrFieldTooLarge, off, 0, st.Fields + 1}
			}
			st.Length++
			b.WriteByte(c)
			return nil
		}
		finish := func(end int, quoted bool, delim bool) error {
			err := emit(Event{Kind: Field, Cell: cell.Cell{Value: b.String(), Quoted: quoted, Start: st.Start, End: end}, Data: b.String()})
			b.Reset()
			st.Length, st.InField, st.Quoted = 0, false, false
			if delim {
				if st.Fields+1 >= s.limits.MaxFields {
					return &PosError{ErrTooManyFields, off, 0, st.Fields + 2}
				}
				st.Fields++
				st.Mode, st.InField, st.Start = startField, true, off+1
			}
			return err
		}
		endRecord := func(end int, quoted bool) (State, error) {
			if err := finish(end, quoted, false); err != nil {
				return st, err
			}
			if err := emit(Event{Kind: Record, RecordDelta: 1}); err != nil {
				return st, err
			}
			st = State{Mode: startField, InField: true, Start: off + 1, Bytes: s.count}
			return st, nil
		}
		switch st.Mode {
		case startField:
			switch c {
			case ',':
				if err := finish(off, false, true); err != nil { return st, err }
			case '"':
				st.Mode, st.Quoted = quoted, true
			case '\n':
				if st.Fields > 0 {
					if err := finish(off, false, false); err != nil { return st, err }
					if err := emit(Event{Kind: Record, RecordDelta: 1}); err != nil { return st, err }
				} else if err := emit(Event{Kind: Blank}); err != nil { return st, err }
				st = State{Mode: startField, InField: true, Start: off + 1}
			case '\r':
				st.Mode, st.Pending = crUnquoted, off
			default:
				st.Mode = unquoted
				if err := add(); err != nil { return st, err }
			}
		case unquoted:
			switch c {
			case ',':
				if err := finish(off, false, true); err != nil { return st, err }
			case '"':
				return fail(ErrQuote, off)
			case '\n':
				if st, err := endRecord(off, false); err != nil { return st, err }
			case '\r':
				st.Mode, st.Pending = crUnquoted, off
			default:
				if err := add(); err != nil { return st, err }
			}
		case quoted:
			if c == '"' {
				st.Mode = afterQuote
			} else {
				st.Mode = quoted
				if err := add(); err != nil { return st, err }
			}
		case afterQuote:
			switch c {
			case ',':
				if err := finish(off+1, true, true); err != nil { return st, err }
			case '"':
				st.Mode = quoted
				if err := add(); err != nil { return st, err }
			case '\n':
				if st, err := endRecord(off+1, true); err != nil { return st, err }
			case '\r':
				st.Mode, st.Pending = crQuoted, off
			default:
				return fail(ErrAfterQuote, off)
			}
		case crUnquoted, crQuoted:
			quotedField := st.Mode == crQuoted
			st.Mode = startField
			if c == '\n' {
				if st, err := endRecord(off, quotedField); err != nil { return st, err }
			} else if c == ',' && !quotedField {
				if err := finish(st.Pending, false, true); err != nil { return st, err }
			} else {
				return fail(ErrLoneCR, st.Pending)
			}
		}
	}
	if final {
		switch st.Mode {
		case quoted:
			return fail(ErrUnterminated, base+len(p))
		case crUnquoted, crQuoted:
			return fail(ErrLoneCR, st.Pending)
		case startField:
			if st.Fields > 0 {
				if err := finish(base+len(p), false, false); err != nil { return st, err }
				if err := emit(Event{Kind: Record}); err != nil { return st, err }
			}
		default:
			if st, err := endRecord(base+len(p), st.Quoted); err != nil { return st, err }
		}
	}
	st.Bytes = s.count
	return st, nil
}
