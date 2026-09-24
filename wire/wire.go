package wire

import "errors"

const (
	Magic   = "OLZ"
	Version = byte(1)

	TagLit    = 0
	TagMatch  = 1
	TagFlush  = 2
	TagEnd    = 3
	MaxVarint = 10
	HeaderLen = 4
)

var (
	ErrBadMagic       = errors.New("wire: bad magic")
	ErrBadVersion     = errors.New("wire: unsupported version")
	ErrTruncated      = errors.New("wire: truncated stream")
	ErrVarintTooLong  = errors.New("wire: varint exceeds 10 bytes")
	ErrVarintRange    = errors.New("wire: varint overflows uint64")
	ErrBadTag         = errors.New("wire: unknown record tag")
	ErrZeroDistance   = errors.New("wire: back-reference distance is zero")
	ErrDistOutput     = errors.New("wire: distance exceeds bytes produced")
	ErrDistWindow     = errors.New("wire: distance exceeds window capacity")
	ErrLengthMismatch = errors.New("wire: end total length mismatch")
	ErrChecksum       = errors.New("wire: checksum mismatch")
	ErrTrailingBytes  = errors.New("wire: trailing bytes after end record")
	ErrOutputLimit    = errors.New("wire: output size limit exceeded")
)

// OffsetError 携带错误发生时的流内字节偏移。
type OffsetError struct {
	Offset int
	Err    error
}

func (e *OffsetError) Error() string { return e.Err.Error() }
func (e *OffsetError) Unwrap() error { return e.Err }

func At(off int, err error) error { return &OffsetError{Offset: off, Err: err} }

func Header() []byte { return []byte{Magic[0], Magic[1], Magic[2], Version} }

func PutUvarint(buf []byte, v uint64) []byte {
	var t [MaxVarint]byte
	n := encode(t[:], v)
	return append(buf, t[:n]...)
}

func encode(b []byte, v uint64) int {
	i := 0
	for v >= 0x80 {
		b[i] = byte(v) | 0x80
		v >>= 7
		i++
	}
	b[i] = byte(v)
	return i + 1
}

func Literal(buf, p []byte) []byte {
	buf = append(buf, TagLit)
	buf = PutUvarint(buf, uint64(len(p)))
	return append(buf, p...)
}

func Match(buf []byte, dist, length int) []byte {
	buf = append(buf, TagMatch)
	buf = PutUvarint(buf, uint64(dist))
	buf = PutUvarint(buf, uint64(length))
	return buf
}

func Flush(buf []byte) []byte { return append(buf, TagFlush) }

func End(buf []byte, totalLen uint64, checksum uint64) []byte {
	buf = append(buf, TagEnd)
	buf = PutUvarint(buf, totalLen)
	buf = PutUvarint(buf, checksum)
	return buf
}
