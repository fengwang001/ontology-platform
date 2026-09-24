// Package wire defines the self-describing LZ77 stream byte format.
// It depends on no other package in this module.
package wire

import "errors"

// Header is the fixed magic+version prefix. Followed by uvar window capacity.
var Header = [4]byte{'L', 'Z', '7', 1}

const (
	TagLiteral byte = 0x00 // uvar len, then len raw bytes (len >= 1)
	TagMatch   byte = 0x01 // uvar dist-1, uvar matchLen-3 (matchLen >= 3)
	TagFlush   byte = 0x02 // no payload
	TagTail    byte = 0x03 // uvar rawLen, uvar CRC-32(IEEE) of raw input
)

// MinMatch is the shortest length represented by a back-reference.
const MinMatch = 3

var (
	ErrBadHeader = errors.New("wire: bad magic or version")
	ErrBadConfig = errors.New("wire: invalid encoded configuration")
	ErrUnknownTag = errors.New("wire: unknown record tag")
	ErrVarint    = errors.New("wire: varint too long or overflows 64 bits")
	ErrTruncated = errors.New("wire: stream truncated")
)

// AppendUvar appends an unsigned LEB128 varint.
func AppendUvar(b []byte, v uint64) []byte {
	for v >= 0x80 {
		b = append(b, byte(v)|0x80)
		v >>= 7
	}
	return append(b, byte(v))
}

// ReadUvar decodes one varint. n is bytes consumed; n==0 with ErrTruncated
// means more input is required.
func ReadUvar(buf []byte) (v uint64, n int, err error) {
	var shift uint
	for n = 0; n < len(buf); n++ {
		c := buf[n]
		if n == 9 && c > 1 { // 10th byte may hold only the top bit
			return 0, 0, ErrVarint
		}
		v |= uint64(c&0x7f) << shift
		if c&0x80 == 0 {
			return v, n + 1, nil
		}
		shift += 7
		if n == 9 {
			return 0, 0, ErrVarint
		}
	}
	return 0, 0, ErrTruncated
}

// AppendHeader appends the stream header declaring the window capacity.
func AppendHeader(b []byte, windowCap int) []byte {
	b = append(b, Header[:]...)
	return AppendUvar(b, uint64(windowCap))
}

// ReadHeader parses the header and returns the window capacity.
func ReadHeader(buf []byte) (windowCap int, n int, err error) {
	if len(buf) < len(Header) {
		return 0, 0, ErrTruncated
	}
	for i := range Header {
		if buf[i] != Header[i] {
			return 0, 0, ErrBadHeader
		}
	}
	v, m, err := ReadUvar(buf[len(Header):])
	if err != nil {
		return 0, 0, err
	}
	if v == 0 || v > uint64(^uint(0)>>1) {
		return 0, 0, ErrBadConfig
	}
	return int(v), len(Header) + m, nil
}

// AppendLiteral appends one literal run.
func AppendLiteral(b, p []byte) []byte {
	b = append(b, TagLiteral)
	b = AppendUvar(b, uint64(len(p)))
	return append(b, p...)
}

// AppendMatch appends one back-reference. dist >= 1, length >= MinMatch.
func AppendMatch(b []byte, dist, length int) []byte {
	b = append(b, TagMatch)
	b = AppendUvar(b, uint64(dist-1))
	return AppendUvar(b, uint64(length-MinMatch))
}

// AppendFlush appends a flush marker.
func AppendFlush(b []byte) []byte { return append(b, TagFlush) }

// AppendTail appends the end-of-stream record.
func AppendTail(b []byte, rawLen int, crc uint32) []byte {
	b = append(b, TagTail)
	b = AppendUvar(b, uint64(rawLen))
	return AppendUvar(b, uint64(crc))
}
