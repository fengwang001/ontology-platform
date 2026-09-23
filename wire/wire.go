// Package wire defines the self-describing LZ77 stream byte format.
package wire

import (
	"errors"
	"hash/fnv"
)

const (
	TagLiteral byte = 0 // uvar length, then raw bytes
	TagMatch   byte = 1 // uvar distance>=1, uvar length>=3
	TagFlush   byte = 2 // no payload
	TagEnd     byte = 3 // uvar original length, uvar FNV-1a64
)

var Magic = [4]byte{'L', 'Z', '7', '7'}
const Version byte = 1

var (
	ErrBadHeader            = errors.New("wire: bad magic or version")
	ErrTruncated            = errors.New("wire: truncated stream")
	ErrBadTag               = errors.New("wire: unknown record tag")
	ErrZeroDistance         = errors.New("wire: match distance is zero")
	ErrDistanceBeyondOutput = errors.New("wire: distance beyond produced bytes")
	ErrDistanceBeyondWindow = errors.New("wire: distance beyond window cap")
	ErrBadMatchLength       = errors.New("wire: match length below minimum")
	ErrVarintTooLong        = errors.New("wire: varint longer than 10 bytes")
	ErrVarintOverflow       = errors.New("wire: varint overflows 64 bits")
	ErrLengthMismatch       = errors.New("wire: trailer length mismatch")
	ErrChecksum             = errors.New("wire: checksum mismatch")
	ErrTrailingBytes        = errors.New("wire: trailing bytes after end")
	ErrOutputLimit          = errors.New("wire: output exceeds limit")
)

// OffsetError carries the byte offset of a parse failure.
type OffsetError struct {
	Err    error
	Offset int
}

func (e *OffsetError) Error() string { return e.Err.Error() }
func (e *OffsetError) Unwrap() error { return e.Err }

// At wraps err with a stream offset.
func At(err error, off int) error {
	if err == nil {
		return nil
	}
	return &OffsetError{err, off}
}

// AppendUvar appends unsigned LEB128.
func AppendUvar(dst []byte, v uint64) []byte {
	for v >= 0x80 {
		dst, v = append(dst, byte(v)|0x80), v>>7
	}
	return append(dst, byte(v))
}

func appendRec(dst []byte, tag byte, nums ...uint64) []byte {
	dst = append(dst, tag)
	for _, v := range nums {
		dst = AppendUvar(dst, v)
	}
	return dst
}

func AppendHeader(dst []byte) []byte { return append(append(dst, Magic[:]...), Version) }
func AppendLiteral(dst, p []byte) []byte {
	return append(appendRec(dst, TagLiteral, uint64(len(p))), p...)
}
func AppendMatch(dst []byte, dist, length int) []byte {
	return appendRec(dst, TagMatch, uint64(dist), uint64(length))
}
func AppendFlush(dst []byte) []byte { return append(dst, TagFlush) }
func AppendEnd(dst []byte, n int, sum uint64) []byte {
	return appendRec(dst, TagEnd, uint64(n), sum)
}

// Checksum returns FNV-1a 64.
func Checksum(p []byte) uint64 {
	h := fnv.New64a()
	h.Write(p)
	return h.Sum64()
}

// Reader parses records sequentially; Feed appends split stream bytes.
type Reader struct {
	b   []byte
	off int
}

func NewReader(b []byte) *Reader { return &Reader{b: append([]byte(nil), b...)} }
func (r *Reader) Feed(b []byte)  { r.b = append(r.b, b...) }
func (r *Reader) Offset() int    { return r.off }
func (r *Reader) Seek(off int)   { r.off = off }

// ReadHeader validates the 5-byte header.
func (r *Reader) ReadHeader() error {
	if len(r.b) < 5 {
		return At(ErrTruncated, r.off)
	}
	if string(r.b[:4]) != string(Magic[:]) || r.b[4] != Version {
		return At(ErrBadHeader, 0)
	}
	r.off = 5
	return nil
}

// ReadTag consumes one record tag.
func (r *Reader) ReadTag() (byte, error) {
	if r.off >= len(r.b) {
		return 0, At(ErrTruncated, r.off)
	}
	t := r.b[r.off]
	if t > TagEnd {
		return 0, At(ErrBadTag, r.off)
	}
	r.off++
	return t, nil
}

// ReadUvar consumes one varint.
func (r *Reader) ReadUvar() (uint64, error) {
	start := r.off
	var x uint64
	for i := 0; i < 10; i++ {
		if r.off >= len(r.b) {
			return 0, At(ErrTruncated, start)
		}
		c := r.b[r.off]
		r.off++
		if i == 9 && c > 1 {
			return 0, At(ErrVarintOverflow, start)
		}
		x |= uint64(c&0x7f) << (7 * i)
		if c < 0x80 {
			return x, nil
		}
}
	return 0, At(ErrVarintTooLong, start)
}

// ReadBytes consumes n raw bytes.
func (r *Reader) ReadBytes(n int) ([]byte, error) {
	if n < 0 || r.off+n > len(r.b) {
		return nil, At(ErrTruncated, r.off)
	}
	p := r.b[r.off : r.off+n]
	r.off += n
	return p, nil
}

// CheckEnd reports trailing bytes after the trailer.
func (r *Reader) CheckEnd() error {
	if r.off != len(r.b) {
		return At(ErrTrailingBytes, r.off)
	}
	return nil
}
