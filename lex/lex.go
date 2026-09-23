// Package lex is a resumable byte-at-a-time shell quotation state machine
// (unquoted / single / double quotes / backslash-pending); no expansion.
package lex

import "errors"

const (
	Sep   = 0 // unquoted blank byte (word separator)
	Begin = 1 // a word starts
	Lit   = 2 // one literal byte of the current word
)

// Item is one machine event; Data is the byte for Lit, else 0.
type Item struct {
	Kind int
	Data byte
}

var (
	ErrUnterminatedSingleQuote = errors.New("lex: unterminated single quote")
	ErrUnterminatedDoubleQuote = errors.New("lex: unterminated double quote")
	ErrTrailingBackslash       = errors.New("lex: trailing backslash")
)

// Error carries the start offset: the opening quote or the backslash.
type Error struct {
	Offset int
	Err    error
}

func (e *Error) Error() string { return e.Err.Error() }
func (e *Error) Unwrap() error { return e.Err }

type state int

const (
	stOut, stIn, stSQ, stDQ, stDQS, stES state = 0, 1, 2, 3, 4, 5
)

// Lexer is a pause/resume machine: Step bytes, then Close.
type Lexer struct {
	state           state
	ret             state // state to return to from quote/escape
	openOff, escOff int
	items           []Item
	count           int // unexported: total bytes processed
}

func isBlank(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r' || b == '\v' || b == '\f'
}

// Items returns and clears buffered events.
func (l *Lexer) Items() []Item {
	out := l.items
	l.items = nil
	return out
}

func (l *Lexer) Count() int         { return l.count }
func (l *Lexer) emit(k int, b byte) { l.items = append(l.items, Item{Kind: k, Data: b}) }

func (l *Lexer) openQuote(off int, q state) {
	l.ret, l.openOff, l.state = l.state, off, q
	if l.ret == stOut {
		l.emit(Begin, 0)
	}
}

// Step feeds one byte with its absolute offset.
func (l *Lexer) Step(b byte, off int) {
	l.count++
	switch l.state {
	case stSQ:
		if b == '\'' {
			l.state = stIn
		} else {
			l.emit(Lit, b)
		}
	case stDQ:
		switch b {
		case '"':
			l.state = stIn
		case '\\':
			l.escOff = off
			l.state = stDQS
		default:
			l.emit(Lit, b)
		}
	case stDQS:
		switch {
		case b == '"' || b == '\\' || b == '$' || b == '`':
			l.emit(Lit, b)
			l.state = stDQ
		case b == '\n':
			l.state = stDQ
		default:
			l.emit(Lit, '\\')
			l.emit(Lit, b)
			l.state = stDQ
		}
	case stES:
		if b == '\n' { // line continuation: drop backslash and newline
			l.state = l.ret
		} else {
			if l.ret == stOut {
				l.emit(Begin, 0)
			}
			l.emit(Lit, b)
			l.state = stIn
		}
	case stIn, stOut:
		switch {
		case isBlank(b):
			if l.state == stIn {
				l.emit(Sep, 0)
				l.state = stOut
			}
		case b == '\'':
			l.openQuote(off, stSQ)
		case b == '"':
			l.openQuote(off, stDQ)
		case b == '\\':
			l.ret = l.state
			l.escOff = off
			l.state = stES
		default:
			if l.state == stOut {
				l.emit(Begin, 0)
				l.state = stIn
			}
			l.emit(Lit, b)
		}
	}
}

// Close signals end of input and returns the terminal error, if any.
func (l *Lexer) Close() error {
	switch l.state {
	case stSQ:
		return &Error{Offset: l.openOff, Err: ErrUnterminatedSingleQuote}
	case stDQ:
		return &Error{Offset: l.openOff, Err: ErrUnterminatedDoubleQuote}
	case stDQS, stES:
		return &Error{Offset: l.escOff, Err: ErrTrailingBackslash}
	}
	return nil
}
