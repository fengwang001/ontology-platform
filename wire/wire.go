// Package wire defines the compressed stream byte format.
package wire

// Magic is the 3-byte stream marker; Version is the format version.
const (
	Magic   = "LZ7"
	Version = byte(1)
)

// Record tags.
const (
	TagLit   = 0x00 // literal run: tag, count varuint, bytes
	TagMatch = 0x01 // back-reference: tag, (dist-1) varuint, (len-MinMatch) varuint
	TagFlush = 0x02 // flush point: tag only
	TagEnd   = 0x03 // end: tag, total length varuint, crc32 varuint
)

// MinMatch is the shortest match emitted as a back-reference.
const MinMatch = 4

// MaxVarintLen is the maximum encoded length of a 64-bit uvarint.
const MaxVarintLen = 10
