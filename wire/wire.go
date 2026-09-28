// Package wire defines the self-describing byte format of the LZ77 stream.
package wire

import "errors"

const (
	TagLiteral = 0
	TagMatch   = 1
	TagFlush   = 2
	TagEnd     = 3
)

// Magic is the 4-byte stream header marker; Version is the only known version.
var Magic = [4]byte{'L', 'Z', 'O', '1'}

const Version = 1

// Header is the fixed header prefix.
var Header = []byte{'L', 'Z', 'O', '1', Version}

// Format errors. They are sentinels so callers can classify corrupt streams.
var (
	ErrBadMagic       = errors.New("wire: bad magic")
	ErrBadVersion     = errors.New("wire: unsupported version")
	ErrVarintTooLong  = errors.New("wire: varint longer than 10 bytes")
	ErrVarintOverflow = errors.New("wire: varint overflows 64 bits")
	ErrUnknownTag     = errors.New("wire: unknown record tag")
	ErrTruncated      = errors.New("wire: truncated stream")
	ErrBadLength      = errors.New("wire: declared length exceeds stream")
	ErrTrailingBytes  = errors.New("wire: trailing bytes after end record")
	ErrTailLength     = errors.New("wire: tail original length mismatch")
	ErrTailChecksum   = errors.New("wire: tail checksum mismatch")
)

// AppendUvarint appends v as an unsigned LEB128-style varint.
func AppendUvarint(dst []byte, v uint64) []byte {
	for v >= 0x80 {
		dst = append(dst, byte(v)|0x80)
		v >>= 7
	}
	return append(dst, byte(v))
}

// Uvarint decodes one varint at the start of b. n is the bytes consumed.
// ErrTruncated means the varint is continued beyond b; ErrVarintTooLong and
// ErrVarintOverflow flag malformed encodings, with n set to the consumed size.
func Uvarint(b []byte) (v uint64, n int, err error) {
	for shift := uint(0); shift < 64; shift += 7 {
		if n >= len(b) {
			return 0, n, ErrTruncated
		}
		c := b[n]
		n++
		if n == 10 && c > 1 { // 64 bits need at most one bit in byte 10.
			return 0, n, ErrVarintOverflow
		}
		if n > 10 {
			return 0, n, ErrVarintTooLong
		}
		v |= uint64(c&0x7f) << shift
		if c&0x80 == 0 {
			return v, n, nil
		}
	}
	return 0, n, ErrVarintTooLong
}

// Record builders. Each appends one record to dst.

func AppendLiteral(dst, p []byte) []byte {
	dst = append(dst, TagLiteral)
	dst = AppendUvarint(dst, uint64(len(p)))
	return append(dst, p...)
}

func AppendMatch(dst []byte, distance, length uint64) []byte {
	dst = append(dst, TagMatch)
	dst = AppendUvarint(dst, distance)
	return AppendUvarint(dst, length)
}

func AppendFlush(dst []byte) []byte { return append(dst, TagFlush) }

func AppendEnd(dst []byte, totalLen, checksum uint64) []byte {
	dst = append(dst, TagEnd)
	dst = AppendUvarint(dst, totalLen)
	return AppendUvarint(dst, checksum)
}

const (
	fnvOffset64 = 14695981039346656037
	fnvPrime64  = 1099511628211
)

// FNV64 is the FNV-1a 64-bit checksum, implemented locally per the rules.
type FNV64 uint64

func NewFNV64() FNV64 { return FNV64(fnvOffset64) }

func (h FNV64) Write(p []byte) FNV64 {
	for _, c := range p {
		h = FNV64(uint64(h) ^ uint64(c))
		h = FNV64(uint64(h) * fnvPrime64)
	}
	return h
}

func (h FNV64) Sum64() uint64 { return uint64(h) }
