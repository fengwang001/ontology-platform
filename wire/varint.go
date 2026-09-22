package wire

// MaxVarintLen64 is the maximum number of bytes used by a 64-bit varint.
const MaxVarintLen64 = 10

// AppendVarint appends the little-endian base-128 varint encoding of v
// to dst and returns the extended slice.
func AppendVarint(dst []byte, v uint64) []byte {
	for v >= 0x80 {
		dst = append(dst, byte(v)|0x80)
		v >>= 7
	}
	return append(dst, byte(v))
}

// VarintLen returns the number of bytes AppendVarint would emit for v.
func VarintLen(v uint64) int {
	n := 1
	for v >= 0x80 {
		v >>= 7
		n++
	}
	return n
}

// ConsumeVarint decodes a varint from buf[start:]. It returns the
// decoded value, the index immediately following the varint and an
// error. A varint that has not terminated after 10 bytes fails with
// ErrVarintTooLong; a truncated final byte fails the same way. On
// error the returned index is start.
func ConsumeVarint(buf []byte, start int) (uint64, int, error) {
	var v uint64
	pos := start
	for shift := uint(0); shift < 64; shift += 7 {
		if pos >= len(buf) {
			return 0, start, NewParseError(ErrVarintTooLong, start)
		}
		b := buf[pos]
		pos++
		if shift == 63 && b > 0x01 {
			return 0, start, NewParseError(ErrVarintTooLong, start)
		}
		v |= uint64(b&0x7f) << shift
		if b&0x80 == 0 {
			return v, pos, nil
		}
	}
	return 0, start, NewParseError(ErrVarintTooLong, start)
}

// ZigZag helpers are intentionally absent: signed integers are the
// caller's concern and are encoded as their unsigned bit pattern.
