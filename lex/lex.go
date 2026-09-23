// Package lex is a resumable byte-at-a-time POSIX shell tokenizer.
// It emits word-boundary and byte events; it never expands anything.
package lex

import (
	"errors"
	"fmt"
)

var (
	ErrSingle = errors.New("unterminated single quote")
	ErrDouble = errors.New("unterminated double quote")
	ErrEscape = errors.New("trailing backslash")
)

// Error reports a tokenization failure at byte Offset.
type Error struct {
	Err    error // one of ErrSingle, ErrDouble, ErrEscape
	Offset int   // opening quote or offending backslash
}

func (e *Error) Error() string { return fmt.Sprintf("lex: %v at byte %d", e.Err, e.Offset) }
func (e *Error) Unwrap() error { return e.Err }

// Op is the kind of a tokenizer Event.
type Op int

const (
	OpBegin Op = iota // a word starts (possibly empty, e.g. "")
	OpEmit            // Byte belongs to the current word
	OpEnd             // the current word ends
)

// Event is one state-machine output; Byte is set only for OpEmit.
type Event struct {
	Op   Op
	Byte byte
}

type state int

const (
	normal state = iota
	single
	double
	escNormal // backslash seen in normal state
	escDouble // backslash seen inside double quotes
)

// Lexer is a resumable tokenizer. The zero value is ready to use.
type Lexer struct {
	st      state
	n       int  // bytes processed (complexity proof)
	pos     int  // offset of the byte being processed
	openPos int  // offset of the opening quote
	escPos  int  // offset of a pending backslash
	open    bool // a word has begun
}

// Count returns how many bytes the state machine has processed.
func (l *Lexer) Count() int { return l.n }

func isBlank(b byte) bool { return b == ' ' || b == '\t' || b == '\n' }

// Step feeds one byte and appends resulting events to ev.
func (l *Lexer) Step(b byte, ev []Event) []Event {
	l.n++
	l.pos = l.n - 1
	switch l.st {
	case normal:
		switch {
		case isBlank(b):
			if l.open {
				l.open = false
				ev = append(ev, Event{Op: OpEnd})
			}
		case b == '\'':
			ev = l.begin(ev)
			l.st, l.openPos = single, l.pos
		case b == '"':
			ev = l.begin(ev)
			l.st, l.openPos = double, l.pos
		case b == '\\':
			l.st, l.escPos = escNormal, l.pos
		default:
			ev = l.begin(ev)
			ev = append(ev, Event{Op: OpEmit, Byte: b})
		}
	case single:
		if b == '\'' {
			l.st = normal
		} else {
			ev = append(ev, Event{Op: OpEmit, Byte: b})
		}
	case double:
		switch b {
		case '"':
			l.st = normal
		case '\\':
			l.st = escDouble
		default:
			ev = append(ev, Event{Op: OpEmit, Byte: b})
		}
	case escNormal:
		l.st = normal
		if b != '\n' { // backslash-newline is a removed continuation
			ev = l.begin(ev)
			ev = append(ev, Event{Op: OpEmit, Byte: b})
		}
	case escDouble:
		l.st = double
		switch b {
		case '$', '`', '"', '\\':
			ev = append(ev, Event{Op: OpEmit, Byte: b})
		case '\n': // removed continuation
		default:
			ev = append(ev, Event{Op: OpEmit, Byte: '\\'}, Event{Op: OpEmit, Byte: b})
		}
	}
	return ev
}

func (l *Lexer) begin(ev []Event) []Event {
	if !l.open {
		l.open = true
		ev = append(ev, Event{Op: OpBegin})
	}
	return ev
}

// Close flushes end-of-input, reporting unterminated constructs.
func (l *Lexer) Close(ev []Event) ([]Event, error) {
	switch l.st {
	case single:
		return ev, &Error{Err: ErrSingle, Offset: l.openPos}
	case double, escDouble:
		return ev, &Error{Err: ErrDouble, Offset: l.openPos}
	case escNormal:
		return ev, &Error{Err: ErrEscape, Offset: l.escPos}
	}
	if l.open {
		l.open = false
		ev = append(ev, Event{Op: OpEnd})
	}
	return ev, nil
}
