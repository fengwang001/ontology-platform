// Package lex is a pausable byte-at-a-time shell token state machine.
package lex

import "errors"

// State identifies how the next input byte should be interpreted.
type State int

const (
	Normal State = iota
	Single
	Double
	Escaped
	DoubleEscaped
)

// Event describes what a word collector should do with one byte.
type Event struct {
	Kind    int
	Payload []byte
}

const (
	None int = iota
	Bytes
	End
)

var (
	// ErrUnterminatedSingle means EOF was reached inside single quotes.
	ErrUnterminatedSingle = errors.New("lex: unterminated single-quoted string")
	// ErrUnterminatedDouble means EOF was reached inside double quotes.
	ErrUnterminatedDouble = errors.New("lex: unterminated double-quoted string")
	// ErrDanglingBackslash means EOF followed an unquoted backslash.
	ErrDanglingBackslash = errors.New("lex: dangling backslash")
)

// SyntaxError is a sentinel-matching error carrying the failure byte offset.
type SyntaxError struct {
	Err    error
	Offset int
}

func (e *SyntaxError) Error() string { return e.Err.Error() }
func (e *SyntaxError) Unwrap() error { return e.Err }

// Machine consumes bytes independently of where input chunks begin or end.
type Machine struct {
	state     State
	inWord    bool
	openPos   int
	slashed   bool
	processed int
}

// State exposes the paused state without allowing callers to mutate it.
func (m *Machine) State() State { return m.state }

// InWord reports whether a word is open at a chunk boundary.
func (m *Machine) InWord() bool { return m.inWord }

// Processed returns the number of bytes consumed by the state machine.
func (m *Machine) Processed() int { return m.processed }

// Step consumes one byte. Offset is its absolute byte offset in the stream.
func (m *Machine) Step(b byte, offset int) Event {
	m.processed++
	switch m.state {
	case Single:
		if b == '\'' {
			m.state = Normal
			return Event{}
		}
		return m.data([]byte{b})

	case Double:
		switch {
		case b == '"':
			m.state = Normal
			return Event{}
		case b == '\\':
			m.state = DoubleEscaped
			return Event{}
		default:
			return m.data([]byte{b})
		}

	case Escaped:
		m.state = Normal
		m.slashed = false
		if b == '\n' {
			return Event{}
		}
		return m.data([]byte{b})

	case DoubleEscaped:
		m.state = Double
		switch {
		case b == '\n':
			return Event{}
		case b == '$' || b == '`' || b == '"' || b == '\\':
			return m.data([]byte{b})
		default:
			return m.data([]byte{'\\', b})
		}

	default:
		switch {
		case b == ' ' || b == '\t' || b == '\n':
			if m.inWord {
				m.inWord = false
				return Event{Kind: End}
			}
			return Event{}
		case b == '\'':
			m.openWord(offset)
			m.state = Single
			m.openPos = offset
			return Event{}
		case b == '"':
			m.openWord(offset)
			m.state = Double
			m.openPos = offset
			return Event{}
		case b == '\\':
			if !m.inWord {
				m.slashed = true
			}
			m.state = Escaped
			m.openPos = offset
			return Event{}
		default:
			return m.data([]byte{b})
		}
	}
}

// Close finalizes a stream and reports an unmatched state.
func (m *Machine) Close() error {
	switch m.state {
	case Single:
		return &SyntaxError{Err: ErrUnterminatedSingle, Offset: m.openPos}
	case Double, DoubleEscaped:
		return &SyntaxError{Err: ErrUnterminatedDouble, Offset: m.openPos}
	case Escaped:
		return &SyntaxError{Err: ErrDanglingBackslash, Offset: m.openPos}
	default:
		return nil
	}
}

func (m *Machine) data(payload []byte) Event {
	m.inWord = true
	m.slashed = false
	return Event{Kind: Bytes, Payload: payload}
}

func (m *Machine) openWord(offset int) {
	if m.slashed {
		m.slashed = false
		return
	}
	if !m.inWord {
		m.inWord = true
		m.openPos = offset
	}
}
