// Package lex implements a resumable POSIX-shell-style byte tokenizer.
// It handles quoting and backslash escapes only: no expansions, no globbing.
package lex

import "fmt"

type state uint8

const (
	stNormal state = iota
	stSingle
	stDouble
	stEscNormal
	stEscDouble
)

// Kind identifies a tokenization error category.
type Kind uint8

const (
	KindUnclosedSingle Kind = iota + 1
	KindUnclosedDouble
	KindTrailingBackslash
)

// Error is a tokenization failure carrying a byte offset.
type Error struct {
	Kind   Kind
	Offset int
}

func (e *Error) Error() string {
	var what string
	switch e.Kind {
	case KindUnclosedSingle:
		what = "unclosed single quote"
	case KindUnclosedDouble:
		what = "unclosed double quote"
	case KindTrailingBackslash:
		what = "trailing backslash"
	}
	return fmt.Sprintf("lex: %s at byte offset %d", what, e.Offset)
}

// Lexer is a byte-at-a-time state machine; Feed may be called with any
// chunking and End finalizes the stream.
type Lexer struct {
	emit    func(string)
	state   state
	buf     []byte
	started bool // current word was touched (byte written or quote opened)
	abs     int  // offset of the next byte
	open    int  // offset of the opening quote or pending backslash
	n       int  // bytes processed
}

// New returns a Lexer that reports each completed word to emit.
func New(emit func(string)) *Lexer { return &Lexer{emit: emit} }

// Processed reports how many input bytes the machine has consumed.
func (l *Lexer) Processed() int { return l.n }

// Feed processes p; every byte passes through the machine exactly once.
func (l *Lexer) Feed(p []byte) {
	for _, b := range p {
		l.step(b)
	}
}

func isBlank(b byte) bool { return b == ' ' || b == '\t' || b == '\n' }

func (l *Lexer) step(b byte) {
	l.n++
	off := l.abs
	l.abs++
	switch l.state {
	case stNormal:
		switch {
		case b == '\'':
			l.state, l.started, l.open = stSingle, true, off
		case b == '"':
			l.state, l.started, l.open = stDouble, true, off
		case b == '\\':
			l.state, l.open = stEscNormal, off
		case isBlank(b):
			l.flush()
		default:
			l.buf = append(l.buf, b)
			l.started = true
		}
	case stSingle:
		if b == '\'' {
			l.state = stNormal
		} else {
			l.buf = append(l.buf, b)
		}
	case stDouble:
		switch b {
		case '"':
			l.state = stNormal
		case '\\':
			l.state = stEscDouble
		default:
			l.buf = append(l.buf, b)
		}
	case stEscNormal:
		l.state = stNormal
		if b != '\n' {
			l.buf = append(l.buf, b)
			l.started = true
		}
	case stEscDouble:
		l.state = stDouble
		switch b {
		case '$', '`', '"', '\\':
			l.buf = append(l.buf, b)
		case '\n':
		default:
			l.buf = append(l.buf, '\\', b)
		}
	}
}

func (l *Lexer) flush() {
	if l.started {
		l.emit(string(l.buf))
		l.buf = l.buf[:0]
		l.started = false
	}
}

// End finalizes the stream, emitting the last word and reporting
// unterminated constructs.
func (l *Lexer) End() error {
	switch l.state {
	case stSingle:
		return &Error{KindUnclosedSingle, l.open}
	case stDouble, stEscDouble:
		return &Error{KindUnclosedDouble, l.open}
	case stEscNormal:
		return &Error{KindTrailingBackslash, l.open}
	}
	l.flush()
	return nil
}
