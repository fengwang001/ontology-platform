// Package wire defines the self-described byte format of the LZ77 stream.
//
// Every record starts with a prefix varint whose low two bits are the tag:
// 0 literal run, 1 back-reference, 2 flush marker, 3 stream end.
package wire

import "errors"

const (
	Magic0    = 0x4F
	Magic1    = 0x4E
	Version   = 1
	TagLit    = 0
	TagMatch  = 1
	TagFlush  = 2
	TagEnd    = 3
	MaxVarint = 10
)

var (
	ErrVarintLong = errors.New("wire: varint longer than 10 bytes or overflow")
	ErrBadMagic   = errors.New("wire: bad magic")
	ErrBadVersion = errors.New("unsupported version")
)

func AppendUvarint(b []byte, v uint64) []byte {
	for v >= 0x80 {
		b = append(b, byte(v)|0x80)
		v >>= 7
	}
	return append(b, byte(v))
}

// ReadUvarint decodes one varint; n==0 means the input ended mid-varint.
func ReadUvarint(b []byte) (v uint64, n int, err error) {
	var x uint64
	for i := 0; i < len(b); i++ {
		c := b[i]
		if i == MaxVarint-1 {
			if c > 1 {
				return 0, i + 1, ErrVarintLong
			}
			x |= uint64(c) << (7 * i)
			return x, i + 1, nil
		}
		if i >= MaxVarint {
			return 0, i + 1, ErrVarintLong
		}
		x |= uint64(c&0x7F) << uint(7*i)
		if c < 0x80 {
			return x, i + 1, nil
		}
	}
	return 0, 0, nil
}

// AppendHeader writes the fixed magic, version and configuration.
func AppendHeader(b []byte, windowCap, chainLimit int) []byte {
	b = append(b, Magic0, Magic1)
	b = AppendUvarint(b, Version)
	b = AppendUvarint(b, uint64(windowCap))
	b = AppendUvarint(b, uint64(chainLimit))
	return b
}

// ReadHeader parses the header; a nil error with n==0 means more bytes needed.
func ReadHeader(b []byte) (windowCap, chainLimit, n int, err error) {
	if len(b) < 2 {
		return 0, 0, 0, nil
	}
	if b[0] != Magic0 || b[1] != Magic1 {
		return 0, 0, 0, ErrBadMagic
	}
	off := 2
	v, k, e := ReadUvarint(b[off:])
	if k == 0 && e == nil {
		return 0, 0, 0, nil
	}
	if e != nil {
		return 0, 0, off, e
	}
	if v != Version {
		return 0, 0, off, ErrBadVersion
	}
	off += k
	wc, k, e := ReadUvarint(b[off:])
	if k == 0 && e == nil {
		return 0, 0, 0, nil
	}
	if e != nil {
		return 0, 0, off, e
	}
	off += k
	cl, k, e := ReadUvarint(b[off:])
	if k == 0 && e == nil {
		return 0, 0, 0, nil
	}
	if e != nil {
		return 0, 0, off, e
	}
	return int(wc), int(cl), off + k, nil
}

func appendRecord(b []byte, tag uint64, vals ...uint64) []byte {
	b = AppendUvarint(b, tag)
	for _, v := range vals {
		b = AppendUvarint(b, v)
	}
	return b
}

func AppendLit(b, lit []byte) []byte {
	b = appendRecord(b, TagLit, uint64(len(lit)))
	return append(b, lit...)
}

func AppendMatch(b []byte, dist, length int) []byte {
	return appendRecord(b, TagMatch, uint64(dist-1), uint64(length-1))
}

func AppendFlush(b []byte) []byte { return appendRecord(b, TagFlush) }

func AppendEnd(b []byte, totalLen int, sum uint64) []byte {
	return appendRecord(b, TagEnd, uint64(totalLen), sum)
}
