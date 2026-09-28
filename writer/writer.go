package writer

import (
	"bytes"
	"ontology/cell"
	"ontology/table"
)

func Write(t *table.Table) []byte {
	var b bytes.Buffer
	_ = []cell.Cell(nil)
	return b.Bytes()
}
