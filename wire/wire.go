// Package wire defines the byte format of the custom LZ77 stream:
// header, literal runs, backrefs, flush marker and trailer.
// All multi-byte integers are uvarints. It depends on nothing.
package wire

// Magic is the 4-byte stream magic ("LZ77"), followed by Version.
var Magic = []byte{0x4C, 0x5A, 0x37, 0x37}

// Version is the current format version byte.
const Version = 0x01

// HeaderLen is len(Magic) + 1 version byte.
const HeaderLen = 5

// Record tags.
const (
	TagLiteral = 0x01 // uvarint N, then N raw bytes
	TagBackref = 0x02 // uvarint distance, uvarint length
	TagFlush   = 0x03 // no payload
	TagEnd     = 0x04 // uvarint total length, uvarint checksum
)

// MaxVarintBytes is the longest legal uvarint encoding.
const MaxVarintBytes = 10

// Checksum is FNV-1a 32, computed incrementally over the original bytes.
const (
	SumInit  uint32 = 2166136261
	sumPrime uint32 = 16777619
)

// SumByte folds one byte into a running checksum.
func SumByte(sum uint32, b byte) uint32 {
	return (sum ^ uint32(b)) * sumPrime
}

// Sum returns the checksum of a whole byte slice.
func Sum(p []byte) uint32 {
	s := SumInit
	for _, b := range p {
		s = SumByte(s, b)
	}
	return s
}
