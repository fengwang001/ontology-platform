package wire

// Field is one decoded field. Payload aliases the input buffer for
// Bytes/Message fields; callers that retain it must copy.
type Field struct {
	Number uint64
	Type   Type
	// Varint holds the payload for Varint fields.
	Varint uint64
	// Payload holds the payload for Bytes/Message fields.
	Payload []byte
	// HeaderLen is the number of bytes before the payload.
	HeaderLen int
}

// AppendHeader appends the encoded header (field number + wire type) of a
// field to dst. For Bytes/Message fields the caller must follow with the
// length varint and the payload.
func AppendHeader(dst []byte, num uint64, t Type) []byte {
	dst = AppendVarint(dst, num)
	return append(dst, byte(t))
}

// AppendField appends a complete field (header + length + payload) to dst.
func AppendField(dst []byte, num uint64, t Type, payload []byte) []byte {
	dst = AppendHeader(dst, num, t)
	if t == Bytes || t == Message {
		dst = AppendVarint(dst, uint64(len(payload)))
	}
	return append(dst, payload...)
}

// AppendVarintField appends a complete varint field to dst.
func AppendVarintField(dst []byte, num uint64, v uint64) []byte {
	dst = AppendHeader(dst, num, Varint)
	return AppendVarint(dst, v)
}

// ReadField decodes one field from the start of buf and returns the field
// and the total number of bytes consumed (header + payload).
//
// All errors are returned as *Error with offsets relative to the start of
// buf; callers parsing at a deeper offset must re-base them.
func ReadField(buf []byte) (Field, int, error) {
	num, n, err := ReadVarint(buf)
	if err != nil {
		return Field{}, 0, &Error{Offset: 0, Err: err}
	}
	if num == 0 {
		return Field{}, 0, &Error{Offset: 0, Err: ErrZeroFieldNumber}
	}
	if n >= len(buf) {
		return Field{}, 0, &Error{Offset: n, Err: ErrTruncated}
	}
	t := Type(buf[n])
	if !t.Valid() {
		return Field{}, 0, &Error{Offset: n, Err: ErrUnknownType}
	}
	f := Field{Number: num, Type: t}
	if t == Varint {
		v, vn, err := ReadVarint(buf[n+1:])
		if err != nil {
			return Field{}, 0, &Error{Offset: n + 1, Err: err}
		}
		f.Varint = v
		f.HeaderLen = n + 1
		return f, n + 1 + vn, nil
	}
	l, ln, err := ReadVarint(buf[n+1:])
	if err != nil {
		return Field{}, 0, &Error{Offset: n + 1, Err: err}
	}
	start := n + 1 + ln
	if l > uint64(len(buf)-start) {
		return Field{}, 0, &Error{Offset: n + 1, Err: ErrLengthOverflow}
	}
	f.Payload = buf[start : start+int(l)]
	f.HeaderLen = start
	return f, start + int(l), nil
}
