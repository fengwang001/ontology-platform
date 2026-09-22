// Package wire implements the low-level encoding primitives of the message
// format: varints, field headers and wire-type handling. It depends on no
// other package in this module.
//
// A field is encoded as:
//
//	[field number varint][wire type 1 byte][length varint, bytes/message only][payload]
package wire

// Type is the wire type of a field.
type Type byte

const (
	// Varint: the payload is a single varint.
	Varint Type = 0
	// Bytes: the payload is a length-prefixed raw byte string.
	Bytes Type = 1
	// Message: the payload is a length-prefixed nested message.
	Message Type = 2
)

// MaxVarintLen is the maximum number of bytes a 64-bit varint may occupy.
const MaxVarintLen = 10

// Valid reports whether t is one of the three defined wire types.
func (t Type) Valid() bool {
	return t == Varint || t == Bytes || t == Message
}

// Field describes a parsed field: its header and the extent of its payload.
type Field struct {
	Number    uint64 // field number
	Type      Type   // wire type
	Value     uint64 // decoded payload, Varint type only
	Header    int    // header length in bytes
	Payload   int    // payload length in bytes
	LenOffset int    // offset of the length prefix within the field, -1 for Varint
}

// Total returns the field's full encoded length in bytes.
func (f Field) Total() int { return f.Header + f.Payload }

// ReadVarint decodes a standard little-endian-base-128 varint from buf.
// It returns the value and the number of bytes consumed. Errors are
// returned as *Error with an offset relative to the start of buf.
func ReadVarint(buf []byte) (v uint64, n int, err error) {
	for i := 0; i < len(buf); i++ {
		if i == MaxVarintLen {
			return 0, 0, &Error{Kind: KindVarintOverflow, Offset: 0}
		}
		b := buf[i]
		if i == MaxVarintLen-1 && b > 1 {
			return 0, 0, &Error{Kind: KindVarintOverflow, Offset: 0}
		}
		v |= uint64(b&0x7f) << uint(7*i)
		if b < 0x80 {
			return v, i + 1, nil
		}
	}
	return 0, 0, &Error{Kind: KindTruncated, Offset: len(buf)}
}

// AppendVarint appends the canonical varint encoding of v to dst.
func AppendVarint(dst []byte, v uint64) []byte {
	for v >= 0x80 {
		dst = append(dst, byte(v)|0x80)
		v >>= 7
	}
	return append(dst, byte(v))
}

// ParseHeader parses one field from the start of buf and reports its
// header and payload extents. It validates that the payload fits in buf.
// Errors are returned as *Error with offsets relative to the start of buf.
func ParseHeader(buf []byte) (Field, error) {
	var f Field
	num, n, err := ReadVarint(buf)
	if err != nil {
		return f, err
	}
	if num == 0 {
		return f, &Error{Kind: KindFieldNumberZero, Offset: 0}
	}
	if n >= len(buf) {
		return f, &Error{Kind: KindTruncated, Offset: n}
	}
	t := Type(buf[n])
	if !t.Valid() {
		return f, &Error{Kind: KindUnknownWireType, Offset: n}
	}
	f.Number = num
	f.Type = t
	f.LenOffset = -1
	hdr := n + 1
	if t == Varint {
		v, m, err := ReadVarint(buf[hdr:])
		if err != nil {
			return f, shift(err, hdr)
		}
		f.Value = v
		f.Header = hdr
		f.Payload = m
		return f, nil
	}
	l, m, err := ReadVarint(buf[hdr:])
	if err != nil {
		return f, shift(err, hdr)
	}
	if l > uint64(len(buf)-hdr-m) {
		return f, &Error{Kind: KindLengthOverflow, Offset: hdr}
	}
	f.Header = hdr + m
	f.Payload = int(l)
	f.LenOffset = hdr
	return f, nil
}

// AppendVarintField appends a complete varint-typed field to dst.
func AppendVarintField(dst []byte, num uint64, v uint64) []byte {
	dst = AppendVarint(dst, num)
	dst = append(dst, byte(Varint))
	return AppendVarint(dst, v)
}

// AppendField appends a complete bytes- or message-typed field to dst.
func AppendField(dst []byte, num uint64, t Type, payload []byte) []byte {
	dst = AppendVarint(dst, num)
	dst = append(dst, byte(t))
	dst = AppendVarint(dst, uint64(len(payload)))
	return append(dst, payload...)
}

func shift(err error, off int) error {
	if e, ok := err.(*Error); ok {
		e.Offset += off
	}
	return err
}
