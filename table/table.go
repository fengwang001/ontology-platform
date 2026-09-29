package table

import (
	"ontology/cell"
	"ontology/lexer"
)

type Limits = lexer.Limits

type Table struct {
	Header []cell.Cell
	Rows   [][]cell.Cell
}

type Parser struct{}

func NewParser(Limits) *Parser { return &Parser{} }

func (p *Parser) Feed([]byte) error { return nil }
func (p *Parser) Close() error      { return nil }
func (p *Parser) Table() *Table     { return nil }

func Parse([]byte, Limits) (*Table, error) { return nil, nil }
