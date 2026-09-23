// Package wire defines the self-describing LZ77 stream byte format.
package wire

// Tag identifies a stream record.
type Tag byte

// Record tags.
const (
	TagLit   Tag = 2
	TagMatch Tag = 3
	TagFlush Tag = 4
	TagEnd   Tag = 5
)

// MaxVarintLen is the maximum allowed length of one unsigned LEB128 integer.
const MaxVarintLen = 10

// AppendUvarint appends x in unsigned LEB128 encoding.
func AppendUvarint(dst []byte, x uint64) []byte {
	return dst
}

// ReadUvarint returns the value, the number of bytes consumed, and whether more
// input is required. A negative consumed length marks a malformed integer.
func ReadUvarint(src []byte) (uint64, int, bool) {
	return 0, 0, false
}
