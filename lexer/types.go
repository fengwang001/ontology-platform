// Package lexer is a resumable byte-at-a-time CSV (RFC 4180 dialect) scanner.
package lexer

import (
	"errors"
	"ontology/cell"
)

// Limits are optional hard caps; a non-positive value means unlimited.
type Limits struct{ MaxFieldBytes, MaxFields, MaxRecords int }

var (
	ErrQuoteInField    = errors.New("lexer: bare '\"' in unquoted field")
	ErrCharsAfterQuote = errors.New("lexer: data after closing quote")
	ErrUnclosedQuote   = errors.New("lexer: unclosed quoted field")
	ErrBareCR          = errors.New("lexer: bare '\\r' not followed by '\\n'")
	ErrFieldTooLarge   = errors.New("lexer: field exceeds MaxFieldBytes")
	ErrTooManyFields   = errors.New("lexer: record exceeds MaxFields")
	ErrTooManyRecords  = errors.New("lexer: table exceeds MaxRecords")
)

// Error wraps a sentinel with the global byte offset of the fault.
type Error struct {
	Err    error
	Offset int64
}

func (e *Error) Error() string { return e.Err.Error() }
func (e *Error) Unwrap() error { return e.Err }

// Kind extracts the sentinel from any lexer error.
func Kind(err error) error {
	var le *Error
	if errors.As(err, &le) {
		return le.Err
	}
	return err
}

// Mode is the assumed start state for a byte range (par uses this).
type Mode int

const (
	MFresh Mode = iota // outside quotes, at field start
	MUnq               // inside an unquoted field
	MQuot              // inside a quoted field
	MAQ                // just saw a closing/escaping quote
)

// Start parameterizes a run: assumed Mode, Size decoded bytes already present
// for a spanning field, and First = that field's first-content offset.
type Start struct {
	Mode  Mode
	Size  int
	First int64
}

// Event is one emitted field; Row marks the last field of a record.
type Event struct {
	Cell cell.Cell
	Row  bool
}
