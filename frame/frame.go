// Package frame length-prefix encodes and decodes variable-length byte fields.
// Record = concat of (4-byte little-endian length + payload); length 0 is a
// legal empty field. Decoding walks fields by offset arithmetic only.
package frame

import (
	"errors"

	"ontology/lenp"
)

var (
	// ErrTruncated is returned when a length prefix declares more payload
	// bytes than the buffer still holds.
	ErrTruncated = errors.New("frame: truncated field: length prefix exceeds remaining bytes")
	// ErrIllegalPrefix is returned when a length prefix cannot be read
	// (fewer than 4 bytes left, i.e. non-aligned/garbled tail, or an
	// out-of-int-range value).
	ErrIllegalPrefix = errors.New("frame: illegal length prefix")
)

// Encode concatenates fields, each prefixed by its 4-byte little-endian length.
// A nil/empty field is encoded as the four zero bytes of a zero-length prefix.
func Encode(fields [][]byte) []byte {
	out := make([]byte, 0)
	for _, f := range fields {
		out = append(out, lenp.PutLength(len(f))...)
		out = append(out, f...)
	}
	return out
}

// Decode reads all fields from buf starting at offset 0. On any malformed
// field it returns (nil, error): nothing partial is ever returned.
func Decode(buf []byte) ([][]byte, error) {
	r := NewReader(buf)
	var fields [][]byte
	for r.Pos() < len(buf) {
		f, err := r.NextField()
		if err != nil {
			return nil, err
		}
		fields = append(fields, f)
	}
	return fields, nil
}

// Reader walks fields of a record by offset arithmetic without scanning
// payload bytes.
type Reader struct {
	buf []byte
	pos int
	// skipPayloadTouched counts payload bytes read during the most recent
	// SkipField call. SkipField never touches payloads, so it is always 0;
	// it is intentionally unexported and absent from every public surface.
	skipPayloadTouched int
}

// NewReader returns a Reader positioned at offset 0 of buf.
func NewReader(buf []byte) *Reader {
	return &Reader{buf: buf}
}

// Pos returns the current read offset in bytes.
func (r *Reader) Pos() int { return r.pos }

// readPrefix reads the length prefix at the current offset without advancing.
func (r *Reader) readPrefix() (int, error) {
	n, err := lenp.GetLength(r.buf[r.pos:])
	if err != nil {
		return 0, errors.Join(ErrIllegalPrefix, err)
	}
	return n, nil
}

// NextField reads the length prefix, returns the next payload field and
// advances the offset by 4+L. A zero-length field yields a non-nil empty
// slice. It fails without advancing when the record is malformed.
func (r *Reader) NextField() ([]byte, error) {
	start := r.pos
	n, err := r.readPrefix()
	if err != nil {
		return nil, err
	}
	if n > len(r.buf)-start-lenp.Width {
		return nil, ErrTruncated
	}
	lo := start + lenp.Width
	r.pos = lo + n
	return r.buf[lo : lo+n : lo+n], nil
}

// SkipField advances past the next field reading ONLY its 4-byte prefix;
// payload bytes are never touched. On failure the cursor does not move.
func (r *Reader) SkipField() error {
	start := r.pos
	r.skipPayloadTouched = 0
	n, err := r.readPrefix()
	if err != nil {
		return err
	}
	if n > len(r.buf)-start-lenp.Width {
		return ErrTruncated
	}
	r.pos = start + lenp.Width + n // offset arithmetic only
	return nil
}
