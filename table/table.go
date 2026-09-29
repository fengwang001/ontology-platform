// Package table assembles CSV records and enforces shape and limits.
package table

import (
	"errors"

	"ontology/cell"
)

// Limits bound parser resource use; zero means unlimited.
type Limits struct {
	MaxFieldBytes int
	MaxFields     int
	MaxRecords    int
}

// Sentinels for shape and limit errors.
var (
	ErrColumnCount    = errors.New("column count mismatch")
	ErrFieldTooLarge  = errors.New("field too large")
	ErrTooManyFields  = errors.New("too many fields in record")
	ErrTooManyRecords = errors.New("too many records")
	ErrTerminal       = errors.New("parser already in terminal error state")
)

// Error is a positioned parse error.
type Error struct {
	Kind   error
	Offset int
	Record int
	Field  int
}

func (e *Error) Error() string { return e.Kind.Error() }

// Is supports errors.Is against the kind sentinel.
func (e *Error) Is(target error) bool { return target == e.Kind }

// Table is the assembled result.
type Table struct {
	Header []cell.Cell
	Rows   [][]cell.Cell
}

// Parser is the streaming CSV parser. One instance is not concurrency safe.
type Parser struct {
	limits Limits
	header bool
	tab    Table
}

// Option configures a Parser.
type Option func(*Parser)

// WithLimits sets resource limits.
func WithLimits(l Limits) Option { return func(p *Parser) { p.limits = l } }

// WithHeader treats the first record as a header.
func WithHeader() Option { return func(p *Parser) { p.header = true } }

// New constructs a streaming parser.
func New(opts ...Option) *Parser {
	p := &Parser{}
	for _, o := range opts {
		o(p)
	}
	return p
}

// Feed supplies more input bytes.
func (p *Parser) Feed(b []byte) error { return nil }

// Close ends input and validates the final record.
func (p *Parser) Close() error { return nil }

// Result returns the assembled table.
func (p *Parser) Result() *Table { return &p.tab }

// BytesProcessed reports raw bytes handled by the state machine.
func (p *Parser) BytesProcessed() int { return 0 }

// Parse parses a complete buffer.
func Parse(b []byte, opts ...Option) (*Table, error) {
	p := New(opts...)
	if err := p.Feed(b); err != nil {
		return p.Result(), err
	}
	if err := p.Close(); err != nil {
		return p.Result(), err
	}
	return p.Result(), nil
}
