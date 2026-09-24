package wire

const (
	TagLiteral = 0
	TagMatch   = 1
	TagFlush   = 2
	TagEnd     = 3
)

var Magic = [3]byte{'L', '7', '7'}
