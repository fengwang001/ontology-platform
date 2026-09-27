package wire

const (
	TagLiteral byte = 0
	TagMatch   byte = 1
	TagFlush   byte = 2
	TagEnd     byte = 3
)

var Magic = [2]byte{'L', '7'}
const Version byte = 1

func MaxUintLen() int { return 10 }
