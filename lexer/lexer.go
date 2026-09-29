// Package lexer is a resumable byte-at-a-time CSV state machine.
package lexer

import (
	"errors"

	"ontology/cell"
)

// Sentinels for the four syntax errors plus bare CR.
var (
	ErrQuoteInUnquoted   = errors.New("quote in unquoted field")
	ErrGarbageAfterQuote = errors.New("bytes after closing quote")
	ErrUnclosedQuote     = errors.New("unclosed quoted field")
	ErrBareCR            = errors.New("bare carriage return")
)

// EventKind enumerates structural events emitted by the machine.
type EventKind int

// Structural events. QuoteOpen marks the opening quote of a field.
const (
	EvData EventKind = iota
	EvQuoteOpen
	EvFieldEnd
	EvRecordEnd
)

// Event is one structural step. Offsets are relative to the scanned byte
// slice origin.
type Event struct {
	Kind   EventKind
	Offset int
	B      byte
}

// Trace is one hypothesis run over a byte slice.
type Trace struct {
	Events []Event
	End    EndState
	Err    *LocalError
}

// EndState is the fine-grained machine state at a chunk end.
type EndState int

// Fine machine states relevant across a seam.
const (
	StBoundary EndState = iota // S: on a field/record boundary
	StUnquoted                 // U: open unquoted field
	StQuoted                   // Q: inside a quoted field
	StQuoteSeen                // A: closing/escape quote pending
	StCRPending                // C: CR pending
)

// LocalError is a syntax error with a slice-relative offset.
type LocalError struct {
	Kind   error
	Offset int
}

func (e *LocalError) Error() string { return e.Kind.Error() }

// Scan runs the machine over p under an outside (atFieldStart=true) or
// inside-quoted (false) entry hypothesis, recording a trace.
func Scan(p []byte, atFieldStart bool) Trace { return Trace{} }

// Lexer is a streaming, resumable CSV machine. It is not safe for
// concurrent use of one instance.
type Lexer struct {
	cells   []cell.Cell
	processed int
}

// NewLexer builds a streaming lexer reporting completed cells and
// terminators through sink functions.
func NewLexer(onCell func(cell.Cell), onRecordEnd func(offset int)) *Lexer {
	return &Lexer{}
}

// Feed advances the machine with more bytes.
func (l *Lexer) Feed(p []byte) error { return nil }

// Close signals end of input.
func (l *Lexer) Close() error { return nil }

// BytesProcessed reports raw bytes consumed by the state machine.
func (l *Lexer) BytesProcessed() int { return l.processed }
