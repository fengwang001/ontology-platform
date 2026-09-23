package wire

import "errors"

const (
	Magic0   = 'L'
	Magic1   = 'Z'
	Magic2   = '7'
	Version  = 1
	TagLit   = 0
	TagRef   = 1
	TagEnd   = 2
	TagFlush = 3
	Header   = "LZ7\x01"
	HashP    = uint64(0x100000001b3)
)

var (
	ErrMagic      = errors.New("wire: bad magic")
	ErrVersion    = errors.New("wire: unsupported version")
	ErrZeroDist   = errors.New("wire: back-reference distance is zero")
	ErrDistPast   = errors.New("wire: distance exceeds produced output")
	ErrDistWindow = errors.New("wire: distance exceeds window capacity")
	ErrVarint     = errors.New("wire: varint too long or overflows 64 bits")
	ErrLength     = errors.New("wire: tail length mismatch")
	ErrChecksum   = errors.New("wire: checksum mismatch")
	ErrTrailing   = errors.New("wire: trailing bytes after end marker")
	ErrTruncated  = errors.New("wire: truncated stream")
	ErrTag        = errors.New("wire: unknown record tag")
)

type OffsetError struct {
	Offset int
	Err    error
}

func (e *OffsetError) Error() string { return e.Err.Error() }
func (e *OffsetError) Unwrap() error { return e.Err }

func At(off int, err error) error { return &OffsetError{Offset: off, Err: err} }

func PutUvar(dst []byte, v uint64) []byte {
	var buf [10]byte
	n := 0
	for v >= 0x80 {
		buf[n] = byte(v) | 0x80
		v >>= 7
		n++
	}
	buf[n] = byte(v)
	return append(dst, buf[:n+1]...)
}

// Uvar reads a uvarint starting at buf[i]. Returns value, end offset, ok.
// ok is false (consumed=0) on truncation, or consumed=-1 when it exceeds
// 10 bytes / overflows 64 bits.
func Uvar(buf []byte, i int) (v uint64, end, consumed int) {
	start := i
	for shift := uint(0); shift < 64; shift += 7 {
		if i >= len(buf) {
			return 0, start, 0
		}
		b := buf[i]
		i++
		if b&0x7f != 0 || shift < 63 {
			v |= uint64(b&0x7f) << shift
		}
		if b < 0x80 {
			return v, i, i - start
		}
	}
	return 0, start, -1
}

func Lit(dst, data []byte) []byte {
	dst = append(dst, TagLit)
	dst = PutUvar(dst, uint64(len(data)))
	return append(dst, data...)
}

func Ref(dst []byte, dist, length int) []byte {
	dst = append(dst, TagRef)
	dst = PutUvar(dst, uint64(dist))
	return PutUvar(dst, uint64(length))
}

func Flush(dst []byte) []byte { return append(dst, TagFlush) }

func End(dst []byte, origLen int, sum uint64) []byte {
	dst = append(dst, TagEnd)
	dst = PutUvar(dst, uint64(origLen))
	return PutUvar(dst, sum)
}

func Hash(seed uint64, p []byte) uint64 {
	h := seed
	for _, b := range p {
		h = h*HashP + uint64(b)
	}
	return h
}

// Merge combines block hashes: hash of X then Y.
func Merge(hx, hy uint64, lenY int) uint64 {
	p, base := uint64(1), HashP
	for n := lenY; n > 0; n >>= 1 {
		if n&1 != 0 {
			p *= base
		}
		base *= base
	}
	return hx*p + hy
}
