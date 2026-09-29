// Package wire 定义自定 LZ77 流的字节格式。不依赖其他包。
package wire

const (
	TagLiteral byte = 0
	TagMatch   byte = 1
	TagFlush   byte = 2
	TagEnd     byte = 3
)

var Header = []byte{'L', 'Z', 1}

