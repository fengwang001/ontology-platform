// Package wire defines the byte format of the LZ77 stream.
package wire

import "errors"

const (
	Magic0 byte = 'O'
	Magic1 byte = 'N'
	Magic2 byte = 'T'
	Magic3 byte = 'L'
	Magic4 byte = 'Z'
	Version     = 1
)

// Record tags.
const (
	TagLiteral byte = 0
	TagMatch   byte = 1
	TagFlush   byte = 2
	TagEnd     byte = 3
)

var (
	ErrVarintTooLong = errors.New("wire: varint longer than 10 bytes")
	ErrVarintRange   = errors.New("wire: varint overflows 64 bits")
)

var crcTable [256]uint64

func init() {
	for n := 0; n < 256; n++ {
		c := uint64(n)
		for k := 0; k < 8; k++ {
			if c&1 != 0 {
				c = 0xC96C5795D7870F42 ^ (c >> 1)
			} else {
				c >>= 1
			}
		}
		crcTable[n] = c
	}
}

// AppendUvarint appends v as an unsigned LEB128.
func AppendUvarint(dst []byte, v uint64) []byte {
	for v >= 0x80 {
		dst = append(dst, byte(v)|0x80)
		v >>= 7
	}
	return append(dst, byte(v))
}

// ReadUvarint reads one unsigned LEB128 starting at pos.
func ReadUvarint(buf []byte, pos int) (uint64, int, error) {
	var x uint64
	for shift := 0; ; shift++ {
		if pos >= len(buf) {
			return 0, pos, nil
		}
		b := buf[pos]
		pos++
		if shift == 10 {
			return 0, pos, ErrVarintTooLong
		}
		if shift == 9 && b > 1 {
			return 0, pos, ErrVarintRange
		}
		x |= uint64(b&0x7F) << (shift * 7)
		if b < 0x80 {
			return x, pos, nil
		}
	}
}

// CRC64 updates the reflected ISO/ECMA CRC (init/xorout 0).
func CRC64(crc uint64, p []byte) uint64 {
	for _, b := range p {
		crc = crcTable[byte(crc)^b] ^ (crc >> 8)
	}
	return crc
}
