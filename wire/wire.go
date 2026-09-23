package wire

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

const (
	TagBlock = iota
	TagLiteral
	TagMatch
	TagFlush
	TagEnd
)

var (
	ErrBadMagic      = errors.New("lz77: bad stream magic")
	ErrBadVersion    = errors.New("lz77: unsupported stream version")
	ErrTruncated     = errors.New("lz77: truncated stream")
	ErrVarintTooLong = errors.New("lz77: varint exceeds ten bytes")
	ErrVarintRange   = errors.New("lz77: varint overflows uint64")
	ErrBadRecord     = errors.New("lz77: unknown record tag")
	ErrZeroDistance  = errors.New("lz77: match distance is zero")
	ErrTooRecent     = errors.New("lz77: match distance exceeds produced bytes")
	ErrWindow        = errors.New("lz77: match distance exceeds window capacity")
	ErrLength        = errors.New("lz77: stream length mismatch")
	ErrChecksum      = errors.New("lz77: checksum mismatch")
	ErrTrailing      = errors.New("lz77: trailing bytes after stream end")
)

type OffsetError struct {
	Offset int
	Err    error
}

func (e *OffsetError) Error() string { return fmt.Sprintf("%v at byte %d", e.Err, e.Offset) }
func (e *OffsetError) Unwrap() error { return e.Err }

type Reader struct {
	data   []byte
	offset int
}

func NewReader(data []byte) *Reader { return &Reader{data: data} }

func (r *Reader) Offset() int { return r.offset }

func (r *Reader) Byte() (byte, error) {
	if r.offset >= len(r.data) {
		return 0, &OffsetError{r.offset, ErrTruncated}
	}
	v := r.data[r.offset]
	r.offset++
	return v, nil
}

func (r *Reader) Bytes(n int) ([]byte, error) {
	if n < 0 || r.offset+n > len(r.data) || r.offset+n < r.offset {
		return nil, &OffsetError{r.offset, ErrTruncated}
	}
	v := r.data[r.offset : r.offset+n]
	r.offset += n
	return v, nil
}

func (r *Reader) Uvarint() (uint64, error) {
	start := r.offset
	v, n := binary.Uvarint(r.data[r.offset:])
	if n == 0 {
		r.offset = len(r.data)
		return 0, &OffsetError{start, ErrTruncated}
	}
	if n < 0 {
		r.offset -= n
		if r.offset-start > 10 {
			return 0, &OffsetError{start, ErrVarintTooLong}
		}
		return 0, &OffsetError{start, ErrVarintRange}
	}
	if n > 10 {
		r.offset += 10
		return 0, &OffsetError{start, ErrVarintTooLong}
	}
	r.offset += n
	return v, nil
}

func (r *Reader) Remaining() []byte { return r.data[r.offset:] }

func Header(w io.Writer) error {
	_, err := w.Write([]byte{'L', 'Z', '7', 1})
	return err
}

func Tag(w io.Writer, tag byte) error {
	_, err := w.Write([]byte{tag})
	return err
}

func Literal(w io.Writer, p []byte) error {
	if len(p) == 0 {
		return nil
	}
	var buf [10]byte
	n := binary.PutUvarint(buf[:], uint64(len(p)))
	if err := Tag(w, TagLiteral); err != nil {
		return err
	}
	if _, err := w.Write(buf[:n]); err != nil {
		return err
	}
	_, err := w.Write(p)
	return err
}

func Match(w io.Writer, distance, length int) error {
	if distance < 1 || length < 1 {
		return ErrBadRecord
	}
	var buf [20]byte
	n := binary.PutUvarint(buf[:], uint64(distance))
	n += binary.PutUvarint(buf[n:], uint64(length))
	if err := Tag(w, TagMatch); err != nil {
		return err
	}
	_, err := w.Write(buf[:n])
	return err
}

func AppendUvarint(p []byte, v uint64) []byte {
	var buf [10]byte
	n := binary.PutUvarint(buf[:], v)
	return append(p, buf[:n]...)
}
