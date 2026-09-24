package table

import (
	"errors"

	"ontology/cell"
	"ontology/lexer"
)

var ErrColumnCount = errors.New("record column count mismatch")

type Table struct {
	Header  []cell.Cell
	Records [][]cell.Cell
}

type Builder struct{ t Table }

func (b *Builder) Field(cell.Cell) {}
func (b *Builder) EndRecord()      {}
func (b *Builder) Table() Table    { return b.t }

func Parse(input []byte, limits lexer.Limits) (Table, error) {
	return Table{}, nil
}
