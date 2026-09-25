// Package wire defines the byte format of the compressed stream:
// header, literal runs, backrefs, flush marks and the stream tail.
// All integers are uvarint encoded.
package wire

import "errors"

const (
	Magic0  = 'O'
	Magic1  = 'L'
	Magic2  = 'Z'
	Version = 1
)

const (
	TagLiteral = 0
	TagBackref = 1
	TagFlush   = 2
	TagEnd     = 3
)

var (
	ErrIncomplete = errors.New("wire: incomplete varint")
	ErrOverflow   = errors.New("wire: varint overflow")
)

func Header() []byte { return []byte{Magic0, Magic1, Magic2, Version} }

func AppendUvarint(dst []byte, v uint64) []byte {
	for v >= 0x80 {
		dst = append(dst, byte(v)|0x80)
		v >>= 7
	}
	return append(dst, byte(v))
}

// ReadUvarint decodes one uvarint, returning the value and bytes consumed.
// ErrIncomplete means more input may complete it; ErrOverflow means the
// encoding exceeds 10 bytes or 64 bits.
func ReadUvarint(b []byte) (uint64, int, error) {
	var v uint64
	for i := 0; i < len(b) && i < 10; i++ {
		c := b[i]
		if i == 9 && c > 1 {
			return 0, 0, ErrOverflow
		}
		v |= uint64(c&0x7f) << (7 * i)
		if c < 0x80 {
			return v, i + 1, nil
		}
	}
	if len(b) >= 10 {
		return 0, 0, ErrOverflow
	}
	return 0, 0, ErrIncomplete
}

func AppendLiteral(dst, p []byte) []byte {
	dst = AppendUvarint(dst, TagLiteral)
	dst = AppendUvarint(dst, uint64(len(p)))
	return append(dst, p...)
}

func AppendBackref(dst []byte, dist, ln int) []byte {
	dst = AppendUvarint(dst, TagBackref)
	dst = AppendUvarint(dst, uint64(dist))
	return AppendUvarint(dst, uint64(ln))
}

func AppendFlush(dst []byte) []byte { return AppendUvarint(dst, TagFlush) }

func AppendEnd(dst []byte, total, sum uint64) []byte {
	dst = AppendUvarint(dst, TagEnd)
	dst = AppendUvarint(dst, total)
	return AppendUvarint(dst, sum)
}

// Checksum is a rolling FNV-1a 64 over the original bytes.
type Checksum uint64

func NewChecksum() Checksum { return 14695981039346656037 }

func (c *Checksum) Add(p []byte) {
	h := uint64(*c)
	for _, b := range p {
		h ^= uint64(b)
		h *= 1099511628211
	}
	*c = Checksum(h)
}

func (c Checksum) Sum() uint64 { return uint64(c) }
