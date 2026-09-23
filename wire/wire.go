// Package wire defines the self-described byte stream format.
package wire

import "errors"

const (
	TagHeader  byte = 0x01
	TagLiteral byte = 0x02
	TagMatch   byte = 0x03
	TagFlush   byte = 0x04
	TagEnd     byte = 0x05
)

const (
	Magic          byte = 0x4F // 'O'
	Version        byte = 1
	MaxVarintLen        = 10
	MinMatch            = 3
	DefaultWindow       = 1 << 16
	DefaultChain        = 32
	MaxMatch            = 1 << 16
)

// Kind identifies a stream error class so callers can distinguish them.
type Kind int

const (
	KindHeader Kind = iota + 1
	KindZeroDistance
	KindDistancePast
	KindDistanceWindow
	KindVarint
	KindLengthMismatch
	KindChecksum
	KindTrailing
)

// Error carries a distinguishable error class and the offending byte offset.
type Error struct {
	Kind   Kind
	Offset int
	Err    error
}

func (e *Error) Error() string { return e.Err.Error() }
func (e *Error) Unwrap() error { return e.Err }

func NewError(k Kind, off int, text string) *Error {
	return &Error{Kind: k, Offset: off, Err: errors.New(text)}
}

// PutUvarint appends an unsigned LEB128 varint to b.
func PutUvarint(b []byte, v uint64) []byte {
	for v >= 0x80 {
		b = append(b, byte(v)|0x80)
		v >>= 7
	}
	return append(b, byte(v))
}

// ReadUvarint consumes one varint from b[off:]. It returns the value, the new
// offset, or a KindVarint error anchored at start.
func ReadUvarint(b []byte, off, start int) (uint64, int, error) {
	var x uint64
	for i := 0; ; i++ {
		if off >= len(b) {
			return 0, off, NewError(KindVarint, start, "wire: varint truncated")
		}
		if i >= MaxVarintLen {
			return 0, off, NewError(KindVarint, start, "wire: varint longer than 10 bytes")
		}
		c := b[off]
		off++
		if i == MaxVarintLen-1 && c > 1 {
			return 0, off, NewError(KindVarint, start, "wire: varint overflows 64 bits")
		}
		x |= uint64(c&0x7F) << (7 * uint(i))
		if c < 0x80 {
			break
		}
	}
	return x, off, nil
}

func encLen(v uint64) int {
	n := 1
	for v >= 0x80 {
		v >>= 7
		n++
	}
	return n
}

// HeaderLen is the encoded header length for the given configuration.
func HeaderLen(windowCap, maxChain uint64) int {
	return 3 + encLen(windowCap) + encLen(maxChain)
}

// PutHeader appends the stream header to b.
func PutHeader(b []byte, windowCap, maxChain uint64) []byte {
	b = append(b, TagHeader, Magic, Version)
	b = PutUvarint(b, windowCap)
	return PutUvarint(b, maxChain)
}

// ParseHeader validates magic/version and returns windowCap, maxChain and the
// offset just past the header.
func ParseHeader(b []byte) (uint64, uint64, int, error) {
	if len(b) == 0 {
		return 0, 0, 0, NewError(KindHeader, 0, "wire: empty stream")
	}
	if b[0] != TagHeader {
		return 0, 0, 1, NewError(KindHeader, 0, "wire: bad header tag")
	}
	if len(b) < 3 || b[1] != Magic {
		return 0, 0, len(b), NewError(KindHeader, 1, "wire: bad magic")
	}
	if b[2] != Version {
		return 0, 0, 3, NewError(KindHeader, 2, "wire: unsupported version")
	}
	wc, off, err := ReadUvarint(b, 3, 3)
	if err != nil {
		return 0, 0, off, err
	}
	mc, off, err := ReadUvarint(b, off, off)
	if err != nil {
		return 0, 0, off, err
	}
	if wc == 0 || mc == 0 {
		return 0, 0, off, NewError(KindHeader, 3, "wire: zero window or chain in header")
}
	return wc, mc, off, nil
}
