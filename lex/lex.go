// Package lex is a byte-at-a-time POSIX shell quoting state machine.
package lex

import "errors"

// EventKind classifies an event emitted by the Machine.
type EventKind uint8

const (
	// EvByte is one literal byte belonging to the current word.
	EvByte EventKind = iota
	// EvMark marks the start or existence of a word (an opened quote),
	// even when no literal bytes follow.
	EvMark
	// EvSep is an unquoted IFS byte that ends the current word.
	EvSep
)

// Event is a single state-machine output.
type Event struct {
	Kind EventKind
	Byte byte
}

var (
	// ErrUnclosedSingle points at the opening single quote.
	ErrUnclosedSingle = errors.New("lex: unclosed single quote")
	// ErrUnclosedDouble points at the opening double quote.
	ErrUnclosedDouble = errors.New("lex: unclosed double quote")
	// ErrTrailingBackslash points at the final backslash.
	ErrTrailingBackslash = errors.New("lex: trailing backslash")
)

// OffsetError wraps a sentinel with the byte offset where the token began.
type OffsetError struct {
	Err    error
	Offset int64
}

func (e *OffsetError) Error() string { return e.Err.Error() }
func (e *OffsetError) Unwrap() error { return e.Err }

// Machine consumes bytes and emits Events. It is safe to pause between
// Feed calls; all pending state survives until the next Feed or Close.
type Machine struct {
	emit     func(Event)
	state    uint8
	quoteOff int64
	escOff   int64
	n        int64
}

const (
	stN  uint8 = iota // unquoted
	stQ1              // inside single quotes
	stQ2              // inside double quotes
	stBN              // unquoted, backslash pending
	stBQ              // inside double quotes, backslash pending
)

// New returns a Machine that reports events through emit.
func New(emit func(Event)) *Machine {
	return &Machine{emit: emit, quoteOff: -1, escOff: -1}
}

// Feed pushes one chunk of bytes through the machine.
func (m *Machine) Feed(p []byte) error {
	for _, c := range p {
		off := m.n
		m.n++
		switch m.state {
		case stN:
			switch {
			case c == ' ' || c == '\t' || c == '\n':
				m.emit(Event{Kind: EvSep})
			case c == '\'':
				m.quoteOff = off
				m.state = stQ1
				m.emit(Event{Kind: EvMark})
			case c == '"':
				m.quoteOff = off
				m.state = stQ2
				m.emit(Event{Kind: EvMark})
			case c == '\\':
				m.escOff = off
				m.state = stBN
			default:
				m.emit(Event{Kind: EvByte, Byte: c})
			}
		case stQ1:
			if c == '\'' {
				m.state = stN
				m.quoteOff = -1
			} else {
				m.emit(Event{Kind: EvByte, Byte: c})
			}
		case stQ2:
			switch c {
			case '"':
				m.state = stN
				m.quoteOff = -1
			case '\\':
				m.state = stBQ
			default:
				m.emit(Event{Kind: EvByte, Byte: c})
			}
		case stBN:
			if c != '\n' {
				m.emit(Event{Kind: EvByte, Byte: c})
			}
			m.state = stN
			m.escOff = -1
		case stBQ:
			switch c {
			case '$', '`', '"', '\\':
				m.emit(Event{Kind: EvByte, Byte: c})
			case '\n':
				// line continuation: drop both bytes
			default:
				m.emit(Event{Kind: EvByte, Byte: '\\'})
				m.emit(Event{Kind: EvByte, Byte: c})
			}
			m.state = stQ2
		}
	}
	return nil
}

// Close must be called after the final Feed to flush and detect errors.
func (m *Machine) Close() error {
	switch m.state {
	case stQ1:
		return &OffsetError{Err: ErrUnclosedSingle, Offset: m.quoteOff}
	case stQ2, stBQ:
		return &OffsetError{Err: ErrUnclosedDouble, Offset: m.quoteOff}
	case stBN:
		return &OffsetError{Err: ErrTrailingBackslash, Offset: m.escOff}
	default:
		return nil
	}
}

// Bytes reports how many input bytes the machine has processed so far.
func (m *Machine) Bytes() int64 { return m.n }
