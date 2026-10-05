// Package cell defines the field value representation shared by the CSV
// packages, plus the common error vocabulary.
package cell

import (
	"errors"
	"fmt"
)

// Cell is one field of a record.
type Cell struct {
	Val    []byte // unescaped content
	Quoted bool   // true if the field was quoted in the source
	Start  int    // byte offset of the field in the original input (inclusive)
	End    int    // byte offset one past the field in the original input
}

// Equal reports field-wise equality including the quoted flag and offsets.
func (c Cell) Equal(o Cell) bool {
	return c.Quoted == o.Quoted && c.Start == o.Start && c.End == o.End && string(c.Val) == string(o.Val)
}

// Error kinds, distinguishable via errors.Is.
var (
	ErrQuoteInUnquoted  = errors.New("csv: quote in unquoted field")
	ErrGarbageAfterQuote = errors.New("csv: unexpected character after closing quote")
	ErrUnclosedQuote    = errors.New("csv: unclosed quoted field")
	ErrBareCR           = errors.New("csv: bare carriage return")
	ErrFieldCount       = errors.New("csv: wrong number of fields in record")
	ErrTooManyFieldBytes = errors.New("csv: field too large")
	ErrTooManyFields    = errors.New("csv: too many fields in record")
	ErrTooManyRecords   = errors.New("csv: too many records")
)

// Error carries a kind plus the position of the failure. Off is a 0-based
// byte offset; Rec and Field are 1-based record and field numbers.
type Error struct {
	Kind  error
	Off   int
	Rec   int
	Field int
}

func (e *Error) Error() string {
	return fmt.Sprintf("%v (offset %d, record %d, field %d)", e.Kind, e.Off, e.Rec, e.Field)
}

// Unwrap exposes Kind so errors.Is(err, cell.ErrBareCR) works.
func (e *Error) Unwrap() error { return e.Kind }
