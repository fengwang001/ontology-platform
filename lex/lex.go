// Package lex is a resumable byte-at-a-time tokenizer state machine for a
// POSIX-shell-like command line. It performs quoting only: no expansion,
// globs, command substitution or redirection are interpreted.
package lex

import "errors"

// Kind enumerates the events emitted by the machine.
type Kind uint8

const (
	Byte  Kind = iota // ByteVal is one literal byte of the current word
	Start             // a word begins
	End               // a word ends
)

// Event is one state-machine output item.
type Event struct {
	Kind Kind
	Byte byte
}

// Error is a tokenization failure. Err is one of the sentinel errors and
// Offset is the byte offset of the opening quote or the trailing backslash.
type Error struct {
	Err    error
	Offset int
}

func (e *Error) Error() string { return e.Err.Error() }
func (e *Error) Unwrap() error { return e.Err }

var (
	// ErrUnclosedSingle reports a single-quoted region open at end of input.
	ErrUnclosedSingle = errors.New("lex: unterminated single-quoted string")
	// ErrUnclosedDouble reports a double-quoted region open at end of input.
	ErrUnclosedDouble = errors.New("lex: unterminated double-quoted string")
	// ErrTrailingBackslash reports input ending with an unpaired backslash.
	ErrTrailingBackslash = errors.New("lex: trailing backslash")
)

type state uint8

const (
	sOutside state = iota // not inside a word
	sWord                 // unquoted, inside a word
	sSingle               // inside '...'
	sDouble               // inside "..."
	sBackU                // '\' outside quotes, next byte pending
	sBackD                // '\' inside double quotes, next byte pending
)

// Machine consumes bytes one at a time and queues the events they produce.
type Machine struct {
	state   state
	inWord  bool
	qOpen   int // offset of the opening quote
	bOffset int // offset of the pending backslash
	queue   []Event
	count   int // total bytes processed
}

func (m *Machine) start() {
	if !m.inWord {
		m.inWord = true
		m.queue = append(m.queue, Event{Kind: Start})
	}
}

func (m *Machine) finish() {
	if m.inWord {
		m.inWord = false
		m.queue = append(m.queue, Event{Kind: End})
	}
}

func (m *Machine) emit(b byte) { m.queue = append(m.queue, Event{Kind: Byte, Byte: b}) }

// Step feeds one byte at offset and buffers the resulting events.
func (m *Machine) Step(b byte, offset int) {
	m.count++
	switch m.state {
	case sSingle:
		if b == '\'' {
			m.state = sWord
		} else {
			m.emit(b)
		}
	case sDouble:
		switch b {
		case '"':
			m.state = sWord
		case '\\':
			m.state, m.bOffset = sBackD, offset
		default:
			m.emit(b)
		}
	case sBackD:
		switch b {
		case '$', '`', '"', '\\':
			m.emit(b)
			m.state = sDouble
		case '\n':
			m.state = sDouble // line continuation: both bytes removed
		default:
			m.emit('\\')
			m.emit(b)
			m.state = sDouble
		}
	case sBackU:
		if b == '\n' {
			m.state = sOutside // line continuation; resume outside-quote rules
		} else {
			m.start()
			m.emit(b)
			m.state = sWord
		}
	default: // sOutside or sWord
		switch {
		case b == ' ' || b == '\t' || b == '\n':
			m.finish()
			m.state = sOutside
		case b == '\'':
			m.start()
			m.state, m.qOpen = sSingle, offset
		case b == '"':
			m.start()
			m.state, m.qOpen = sDouble, offset
		case b == '\\':
			m.state, m.bOffset = sBackU, offset
		default:
			m.start()
			m.emit(b)
			m.state = sWord
		}
	}
}

// Events drains and returns queued events; it must be called until empty
// before the next Step.
func (m *Machine) Events() []Event {
	out := m.queue
	m.queue = nil
	return out
}

// End marks end of input, emitting a final End if a word is open and
// returning the relevant unclosed/trailing-backslash error.
func (m *Machine) End() []Event {
	switch m.state {
	case sSingle:
		m.queue = append(m.queue, Event{Kind: End})
	case sDouble, sBackD:
		m.queue = append(m.queue, Event{Kind: End})
	case sBackU:
		m.finish()
	default:
		m.finish()
	}
	return m.Events()
}

// Err converts the pending end-of-input state into an error, or nil.
func (m *Machine) Err() error {
	switch m.state {
	case sSingle:
		return &Error{Err: ErrUnclosedSingle, Offset: m.qOpen}
	case sDouble, sBackD:
		return &Error{Err: ErrUnclosedDouble, Offset: m.qOpen}
	case sBackU:
		return &Error{Err: ErrTrailingBackslash, Offset: m.bOffset}
	default:
		return nil
	}
}

// Processed reports the total number of bytes handed to Step.
func (m *Machine) Processed() int { return m.count }
