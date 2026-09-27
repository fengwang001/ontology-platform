// Package wire defines the byte format of the compressed stream.
package wire

const (
	Magic0 byte = 0x4C
	Magic1 byte = 0x5A
	Magic2 byte = 0x37
	Magic3 byte = 0x37
	Version byte = 1
)

const (
	TagLiteral byte = 1
	TagMatch   byte = 2
	TagFlush   byte = 3
	TagEnd     byte = 4
)
