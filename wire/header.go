package wire

// Header describes one parsed field header. PayloadStart/PayloadEnd
// delimit the payload inside the buffer handed to ConsumeHeader.
type Header struct {
	Number       uint64
	Type         byte
	PayloadStart int
	PayloadEnd   int
}

// IsLengthPrefixed reports whether t carries a varint length prefix.
func IsLengthPrefixed(t byte) bool { return t == Bytes || t == Message }

// ValidType reports whether t is one of the three known wire types.
func ValidType(t byte) bool { return t <= Message }

// ConsumeHeader parses a single field header starting at buf[start]
// and, for length-prefixed types, validates the length prefix against
// the bytes actually remaining. fieldPayloadLimit (<=0 means no limit)
// bounds an individual length-prefixed payload.
//
// Error offsets:
//   - ErrFieldNumberZero / ErrVarintTooLong: at the field-number varint,
//   - ErrUnknownWireType: at the wire-type byte,
//   - ErrVarintTooLong: at the length varint,
//   - ErrLengthOverflow: at the length varint,
//   - ErrFieldSizeLimit: at the length varint.
func ConsumeHeader(buf []byte, start int, fieldPayloadLimit int) (Header, int, error) {
	num, pos, err := ConsumeVarint(buf, start)
	if err != nil {
		return Header{}, start, err // offset already set by ConsumeVarint
	}
	if num < MinFieldNumber {
		return Header{}, start, NewParseError(ErrFieldNumberZero, pos)
	}
	if pos >= len(buf) {
		return Header{}, start, NewParseError(ErrUnknownWireType, pos)
	}
	t := buf[pos]
	typePos := pos
	pos++

	h := Header{Number: num, Type: t}
	if !ValidType(t) {
		return Header{}, start, NewParseError(ErrUnknownWireType, typePos)
	}
	if !IsLengthPrefixed(t) {
		_, afterVal, verr := ConsumeVarint(buf, pos)
		if verr != nil {
			return Header{}, start, verr // offset is the value varint start
		}
		h.PayloadStart = pos
		h.PayloadEnd = afterVal
		return h, h.PayloadEnd, nil
	}

	length, afterLen, lerr := ConsumeVarint(buf, pos)
	if lerr != nil {
		return Header{}, start, lerr
	}
	if fieldPayloadLimit > 0 && length > uint64(fieldPayloadLimit) {
		return Header{}, start, NewParseError(ErrFieldSizeLimit, pos)
	}
	end := afterLen + int(length)
	if length > uint64(len(buf)-afterLen) || end < afterLen {
		return Header{}, start, NewParseError(ErrLengthOverflow, pos)
	}
	h.PayloadStart = afterLen
	h.PayloadEnd = end
	return h, end, nil
}

// AppendHeader appends a field header (number, type and, for
// length-prefixed types, the given length) to dst.
func AppendHeader(dst []byte, num uint64, t byte, length int) []byte {
	dst = AppendVarint(dst, num)
	dst = append(dst, t)
	if IsLengthPrefixed(t) {
		dst = AppendVarint(dst, uint64(length))
	}
	return dst
}
