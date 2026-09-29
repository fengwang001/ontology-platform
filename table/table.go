// Package table assembles lexer events into records with column checks.
package table

import (
	"errors"

	"ontology/cell"
	"ontology/lexer"
)

var (
	ErrArity       = errors.New("table: record field count differs from header")
	ErrTooMany     = errors.New("table: record count exceeds MaxRecords")
	ErrClosedParse = errors.New("table: parser is in terminal state")
)

// ParseError annotates a lexer/table error with global coordinates.
type ParseError struct {
	Err    error
	Offset int
	Record int
	Field  int
}

func (e *ParseError) Error() string { return "" }
func (e *ParseError) Unwrap() error { return e.Err }

// Limits configure a streaming parser.
type Limits struct {
	MaxFieldBytes      int
	MaxFieldsPerRecord int
	MaxRecords         int
}

// Table is an accepted CSV table; the first record is the header.
type Table struct {
	Records [][]cell.Cell
}

// Parser is a feed/close streaming parser (not safe for concurrent use).
type Parser struct {
	t Table
}

// NewParser constructs a streaming parser.
func NewParser(lim Limits) *Parser { return &Parser{} }

// Feed appends bytes.
func (p *Parser) Feed(b []byte) error { return nil }

// Close ends the stream.
func (p *Parser) Close() error { return nil }

// Result returns accepted records (complete records already parsed).
func (p *Parser) Result() Table { return p.t }

var _ = lexer.StField
