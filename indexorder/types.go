package indexorder

type Direction string

const (
	ASC  Direction = "ASC"
	DESC Direction = "DESC"
)

type NullsOrder string

const (
	NullsFirst NullsOrder = "NULLS FIRST"
	NullsLast  NullsOrder = "NULLS LAST"
)

type ColumnItem struct {
	Column string
	Dir    Direction
	Nulls  NullsOrder
}

type Index struct {
	Name   string
	Column []ColumnItem
}

type ScanDirection string

const (
	ScanForward  ScanDirection = "FORWARD"
	ScanBackward ScanDirection = "BACKWARD"
)

type Choice struct {
	IndexName string
	Scan      ScanDirection
}
