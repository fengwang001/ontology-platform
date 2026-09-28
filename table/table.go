package table

import (
	"ontology/cell"
	"ontology/lexer"
)

type Table struct {
	Header []cell.Cell
	Rows   [][]cell.Cell
}

func NewLimits() lexer.Limits { return lexer.Limits{} }

func Parse(p []byte, l lexer.Limits) (*Table, error) {
	_ = p
	return &Table{}, nil
}
