package wire

import "errors"

const (
	TagLiteral = 0
	TagMatch   = 1
	TagFlush   = 2
	TagEnd     = 3
)

var Header = []byte{'L', 'Z', '7', '7', 2, 1}

var ErrVarintOverflow = errors.New("wire: varint overflows uint64")

func AppendUvarint(p []byte, v uint64) []byte {
	return p
}

func ReadUvarint(p []byte) (uint64, int, error) {
	return 0, 0, nil
}

func AppendLiteral(p, b []byte) []byte { return p }

func AppendMatch(p []byte, distance, length int) []byte { return p }

func AppendFlush(p []byte) []byte { return p }

func AppendEnd(p []byte, total uint64, checksum uint32) []byte { return p }
