// Package lexer implements a resumable byte-oriented CSV state machine.
package lexer

import "ontology/cell"

// Limits constrain parser resource use.
type Limits struct {
	MaxFieldBytes int
	MaxFieldCount int
	MaxRecords    int
}

// Position locates a syntax or limit error in the logical document.
type Position struct {
	Offset int
	Record int
	Field  int
}

// Error is a recognizable parser error.
type Error struct {
	Kind error
	Pos  Position
}

// Event marks a closed field or record boundary.
type Event struct {
	Cell   cell.Cell
	Record bool
}

// Parser feeds bytes to the state machine.
type Parser struct{}

// New returns a parser that sends complete events to emit.
func New(_ Limits, _ func(Event)) *Parser { return &Parser{} }

// Feed accepts one arbitrary byte fragment.
func (p *Parser) Feed(_ []byte) error { return nil }

// Close finalizes an input stream.
func (p *Parser) Close() error { return nil }

// Processed reports bytes consumed by the state machine.
func (p *Parser) Processed() int { return 0 }
