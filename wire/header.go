package wire

// Header is a decoded field header: field number and wire type.
type Header struct {
	Number uint64
	Type   Type
	Next   int // offset just past the header
}

// AppendHeader appends the encoding of a field header to dst.
func AppendHeader(dst []byte, num uint64, t Type) []byte {
	dst = AppendVarint(dst, num)
	return append(dst, byte(t))
}

// ReadHeader reads a field header starting at buf[off].
func ReadHeader(buf []byte, off int) (Header, error) {
	num, next, err := ReadVarint(buf, off)
	if err != nil {
		return Header{}, err
	}
	if num == 0 {
		return Header{}, &Error{Kind: KindFieldNumberZero, Offset: off}
	}
	if next >= len(buf) {
		return Header{}, &Error{Kind: KindTruncated, Offset: next}
	}
	t := Type(buf[next])
	next++
	if !t.Valid() {
		return Header{}, &Error{Kind: KindUnknownWireType, Offset: next - 1}
	}
	return Header{Number: num, Type: t, Next: next}, nil
}

// ReadLength reads a length prefix starting at buf[off] and validates
// it against the bytes remaining in buf.
func ReadLength(buf []byte, off int) (length int, next int, err error) {
	v, n, err := ReadVarint(buf, off)
	if err != nil {
		return 0, 0, err
	}
	if v > uint64(len(buf)-n) {
		return 0, 0, &Error{Kind: KindLengthOverflow, Offset: off}
	}
	return int(v), n, nil
}
