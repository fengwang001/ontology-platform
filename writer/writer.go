package writer

import (
	"bytes"
	"ontology/cell"
)

type Record = []cell.Cell

func Write(_ [][]cell.Cell) []byte { return nil }
var _ = bytes.Buffer{}
