package table

import "ontology/cell"

type Record []cell.Field
type Table struct{ rows []Record }
type Parser struct{}
type Limits struct{}
