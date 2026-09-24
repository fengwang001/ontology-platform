// Package wire defines the compressed byte-stream format and its varints.
package wire

import "errors"

// Record tags.
const (
	TagLit   = 0x00 // literal run: tag, varint n, n bytes
	TagMatch = 0x01 // back reference: tag, varint distance, varint length
	TagFlush = 0x02 // flush marker: tag only
	TagEnd   = 0x03 // stream end: tag, varint totalLen, varint checksum
)

// Header is the fixed stream prefix.
var Header = []byte{'O', 'N', 'T', 'O', 0x01}

// Sentinel errors; corruption classes are wrapped in CorruptError with Kind
// equal to one of these so callers can both print and classify them.
var (
	ErrBadMagic   = errors.New("wire: bad magic or version")
	ErrDistZero   = errors.New("wire: match distance is zero")
	ErrDistOutput = errors.New("wire: match distance exceeds bytes emitted")
	ErrDistWindow = errors.New("wire: match distance exceeds window capacity")
	ErrVarInt     = errors.New("wire: varint too long or overflows 64 bits")
	ErrLength     = errors.New("wire: footer length mismatch")
	ErrChecksum   = errors.New("wire: footer checksum mismatch")
	ErrTrailing   = errors.New("wire: trailing bytes after footer")
	ErrTruncated  = errors.New("wire: truncated stream")
	ErrOutputCap  = errors.New("wire: output length exceeds configured limit")
)

// CorruptError pairs a corruption class with the byte offset in the stream.
type CorruptError struct {
	Kind   error
	Offset int
}

func (e *CorruptError) Error() string { return e.Kind.Error() + " at byte " + itoa(e.Offset) }
func (e *CorruptError) Unwrap() error { return e.Kind }

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// AppendUvarint appends x in unsigned LEB128 (max 10 bytes for 64-bit values).
func AppendUvarint(dst []byte, x uint64) []byte {
	for x >= 0x80 {
		dst = append(dst, byte(x)|0x80)
		x >>= 7
	}
	return append(dst, byte(x))
}

// ReadUvarint consumes one LEB128 varint from b. consumed is the number of
// bytes used; ok=false means the input ends mid-varint (caller retries after
// more bytes arrive) unless more than 10 bytes carry high bits or the value
// overflows 64 bits, in which case bad=true.
func ReadUvarint(b []byte) (v uint64, consumed int, ok, bad bool) {
	for shift := uint(0); shift < 64; shift += 7 {
		if consumed >= len(b) {
			return 0, consumed, false, false
		}
		c := b[consumed]
		consumed++
		if shift == 63 && c > 1 {
			return 0, consumed, false, true
		}
		v |= uint64(c&0x7F) << shift
		if c < 0x80 {
			return v, consumed, true, false
		}
	}
	return 0, consumed, false, true
}

const (
	fnvOffset = 14695981039346656037
	fnvPrime  = 1099511628211
)

// Checksum is FNV-1a 64 over the decompressed payload.
func Checksum(data []byte) uint64 {
	h := uint64(fnvOffset)
	for _, c := range data {
		h ^= uint64(c)
		h *= fnvPrime
	}
	return h
}
