// Package lexer is a resumable byte-at-a-time RFC 4180-dialect state machine.
package lexer

import (
	"errors"
	"fmt"

	"ontology/cell"
)

// Syntax error kinds; all four are mutually distinguishable via errors.Is.
var (
	ErrBareQuote      = errors.New("lexer: bare '\"' in unquoted field")
	ErrQuoteAfterClose = errors.New("lexer: data after closing quote")
	ErrUnclosedQuote  = errors.New("lexer: unterminated quoted field")
	ErrBareCR         = errors.New("lexer: bare '\\r' not followed by '\\n'")
)

// ErrFieldCount means a record's field count differs from the first record.
var ErrFieldCount = errors.New("table: field count mismatch")

// Limits: zero means unlimited.
type Limits struct {
	MaxFieldBytes int
	MaxFields     int
	MaxRecords    int
}

// Position locates an error: byte offset from 0, 1-based record/field numbers.
type Position struct {
	Offset  int
	Record  int
	Field   int
}

func (p Position) String() string {
	return fmt.Sprintf("offset=%d record=%d field=%d", p.Offset, p.Record, p.Field)
}

// Error carries a kind, a position and an optional detail (e.g. limit size).
type Error struct {
	Kind error
	Pos  Position
	Got  int
	Want int
}

func (e *Error) Error() string { return fmt.Sprintf("%v at %s", e.Kind, e.Pos) }
func (e *Error) Unwrap() error { return e.Kind }

// Sink receives lexer events in stream order.
type Sink interface {
	Field(cell.Cell) error
	RecordEnd(recNo int, nFields int) error
}

// State is the lexer state, exported for the parallel splitter.
type State int

const (
	StFieldStart State = iota
	StBare
	StQuoted
	StQuoteSeen
	StCRPending
)

// Lexer feeds bytes incrementally; a single instance is not goroutine-safe.
type Lexer struct {
	sink    Sink
	lim     Limits
	state   State
	baseOff int
	recNo   int
	fieldNo int
	started bool // a field for the current record was already emitted
	buf     []byte
	fstart  int
	quoted  bool
	flen    int
	nfield  int
	closed  bool
	fatal   error
	steps   int64
}

// New builds a lexer. baseOff/baseRec/baseField rebase positions (used by par).
func New(sink Sink, lim Limits, baseOff, baseRec, baseField int) *Lexer {
	return &Lexer{sink: sink, lim: lim, baseOff: baseOff,
		recNo: baseRec, fieldNo: baseField - 1, fstart: baseOff}
}

// Steps reports how many bytes the state machine processed (exactly once each).
func (l *Lexer) Steps() int64 { return l.steps }

func (l *Lexer) fail(kind error, off, rec, field int) error {
	if l.fatal == nil {
		l.fatal = &Error{Kind: kind, Pos: Position{off, rec, field}}
	}
	l.closed = true
	return l.fatal
}

func (l *Lexer) limit(kind error, off int) error {
	if l.fatal == nil {
		l.fatal = &Error{Kind: kind, Pos: Position{off, l.recNo + 1, l.fieldNo + 1}}
	}
	l.closed = true
	return l.fatal
}

// Feed pushes more bytes; after a fatal error the same error is returned.
func (l *Lexer) Feed(p []byte) error {
	if l.closed {
		if l.fatal != nil {
			return l.fatal
		}
		return errors.New("lexer: feed after close")
	}
	for i, b := range p {
		l.steps++
		off := l.baseOff + i
		if err := l.step(b, off); err != nil {
			return err
		}
}
	l.baseOff += len(p)
	return nil
}
