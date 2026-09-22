package wire

// WireType is the type byte of a field header.
type WireType byte

const (
	// Varint: payload is a single varint.
	Varint WireType = 0
	// Bytes: payload is a length-prefixed raw byte string.
	Bytes WireType = 1
	// Message: payload is a length-prefixed nested message.
	Message WireType = 2
)

// Valid reports whether t is one of the three known wire types.
func (t WireType) Valid() bool { return t <= Message }

// Header is a decoded field header: field number plus wire type.
type Header struct {
	Field uint64
	Type  WireType
}

// AppendHeader appends the encoding of a field header to dst.
func AppendHeader(dst []byte, field uint64, t WireType) []byte {
	dst = AppendVarint(dst, field)
	return append(dst, byte(t))
}

// ReadHeader decodes a field header starting at buf[off] and returns
// the offset just past it. Errors: KindFieldNumberZero,
// KindUnknownWireType, or any varint error from ReadVarint.
func ReadHeader(buf []byte, off int) (Header, int, error) {
	num, next, err := ReadVarint(buf, off)
	if err != nil {
		return Header{}, 0, err
	}
	if num == 0 {
		return Header{}, 0, &Error{Kind: KindFieldNumberZero, Offset: off}
	}
	if next >= len(buf) {
		return Header{}, 0, &Error{Kind: KindTruncated, Offset: next}
	}
	t := WireType(buf[next])
	if !t.Valid() {
		return Header{}, 0, &Error{Kind: KindUnknownWireType, Offset: next}
	}
	return Header{Field: num, Type: t}, next + 1, nil
}

// ReadLength decodes a length prefix at buf[off] and verifies that
// the claimed length fits inside buf. It returns the length and the
// offset of the first payload byte. Error: KindLength when the claim
// exceeds the remaining bytes.
func ReadLength(buf []byte, off int) (int, int, error) {
	v, next, err := ReadVarint(buf, off)
	if err != nil {
		return 0, 0, err
	}
	if v > uint64(len(buf)-next) {
		return 0, 0, &Error{Kind: KindLength, Offset: off}
	}
	return int(v), next, nil
}
