package wire

import "math"

// MaxFieldNum is the largest valid field number.
const MaxFieldNum = math.MaxUint32

// Header is the decoded prefix of one field.
type Header struct {
	Num  uint32 // field number
	Type Type   // wire type
	Len  int    // payload length; only valid when Type.HasLength()
	Size int    // total header size in bytes (num + type + length)
}

// DecodeHeader parses the field header at the start of buf.
//
// buf must contain the remainder of the enclosing message, so a length
// prefix is validated against the bytes that actually remain. Returned
// error offsets are relative to the start of buf; callers add their
// own base offset with AddOffset.
func DecodeHeader(buf []byte) (Header, error) {
	var h Header
	num, n, err := DecodeVarint(buf)
	if err != nil {
		return h, err
	}
	if num == 0 {
		return h, &ParseError{Err: ErrFieldNumZero, Offset: 0}
	}
	if num > MaxFieldNum {
		return h, &ParseError{Err: ErrFieldNumTooLarge, Offset: 0}
	}
	if n >= len(buf) {
		return h, &ParseError{Err: ErrTruncatedHeader, Offset: n}
	}
	typ := Type(buf[n])
	if !typ.Valid() {
		return h, &ParseError{Err: ErrUnknownWireType, Offset: n}
	}
	h.Num = uint32(num)
	h.Type = typ
	h.Size = n + 1
	if !typ.HasLength() {
		return h, nil
	}
	l, m, err := DecodeVarint(buf[h.Size:])
	if err != nil {
		return h, AddOffset(err, h.Size)
	}
	if l > uint64(len(buf)-h.Size-m) {
		return h, &ParseError{Err: ErrLengthOverflow, Offset: h.Size}
	}
	h.Len = int(l)
	h.Size += m
	return h, nil
}

// AppendHeader appends the encoding of a field header to dst. length
// is only emitted for length-prefixed wire types.
func AppendHeader(dst []byte, num uint32, typ Type, length int) []byte {
	dst = AppendVarint(dst, uint64(num))
	dst = append(dst, byte(typ))
	if typ.HasLength() {
		dst = AppendVarint(dst, uint64(length))
	}
	return dst
}
