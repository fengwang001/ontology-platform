// Package wire defines the custom LZ77 stream byte format.
package wire

import "fmt"

// Magic is the 4-byte stream header magic "ONTL".
var Magic = [4]byte{'O', 'N', 'T', 'L'}

// Version is the supported format version.
const Version = 1

// Record tags.
const (
	TagLit     = 0 // literal run: tag, varint n, n bytes
	TagMatch   = 1 // back reference: tag, varint dist, varint length
	TagFlush   = 2 // flush boundary: tag only
	TagTrailer = 3 // end of stream: tag, varint totalLen, varint crc32
)

// MaxVarintLen is the maximum legal length of an unsigned LEB128 varint.
const MaxVarintLen = 10

// AppendUvarint appends x as an unsigned LEB128 varint.
func AppendUvarint(b []byte, x uint64) []byte {
	for x >= 0x80 {
		b = append(b, byte(x)|0x80)
		x >>= 7
	}
	return append(b, byte(x))
}

// DecodeError is a format error located at a byte offset in the stream.
type DecodeError struct {
	Offset int64
	Kind   Kind
}

// Kind enumerates distinguishable corruption classes.
type Kind int

const (
	KindTruncated Kind = iota
	KindBadHeader
	KindVarintOverflow
	KindBadTag
	KindZeroDistance
	KindDistanceBeyondOutput
	KindDistanceBeyondWindow
	KindLengthMismatch
	KindChecksumMismatch
	KindTrailingBytes
	KindOutputLimit
)

var kindText = map[Kind]string{
	KindTruncated:             "truncated stream",
	KindBadHeader:             "bad magic or version",
	KindVarintOverflow:        "varint too long or overflows 64 bits",
	KindBadTag:                "unknown record tag",
	KindZeroDistance:          "back-reference distance is zero",
	KindDistanceBeyondOutput:  "distance beyond bytes already output",
	KindDistanceBeyondWindow:  "distance beyond window capacity",
	KindLengthMismatch:        "trailer length mismatch",
	KindChecksumMismatch:      "checksum mismatch",
	KindTrailingBytes:         "trailing bytes after trailer",
	KindOutputLimit:           "output exceeds configured limit",
}

func (e *DecodeError) Error() string {
	return fmt.Sprintf("wire: %s at byte offset %d", kindText[e.Kind], e.Offset)
}

// ReadUvarint reads one varint from b starting at off. It returns the value,
// the offset just past it, and a *DecodeError on truncation/overflow.
func ReadUvarint(b []byte, off int64) (uint64, int64, error) {
	var x uint64
	var s uint
	start := off
	for i := 0; ; i++ {
		if off >= int64(len(b)) {
			return 0, off, &DecodeError{Offset: start, Kind: KindTruncated}
		}
		c := b[off]
		off++
		if i == MaxVarintLen-1 {
			if c&0x80 != 0 || c > 1 {
				return 0, off, &DecodeError{Offset: start, Kind: KindVarintOverflow}
			}
			x += uint64(c) << s
			return x, off, nil
		}
		x += uint64(c&0x7f) << s
		if c&0x80 == 0 {
			return x, off, nil
		}
		s += 7
	}
}
