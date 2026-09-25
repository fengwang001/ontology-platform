package table

import (
	"errors"

	"ontology/cell"
	"ontology/lexer"
)

var ErrFieldCount = errors.New("field count mismatch")

type Limits = lexer.Limits

type Table struct {
	Header []cell.Cell
	Rows   [][]cell.Cell
}

type Parser struct{}

func NewParser(limits Limits) *Parser { return &Parser{} }
func (p *Parser) Feed(b []byte) error { return nil }
func (p *Parser) Close() error        { return nil }
func (p *Parser) Table() Table        { return Table{} }
