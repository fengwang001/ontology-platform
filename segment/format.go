package segment

import (
	"errors"
	"fmt"

	"ontology/bitpack"
	"ontology/zone"
)

// Stage names reported by CorruptError.
const (
	StageHeader = "header"
	StageStats  = "stats"
	StageNulls  = "null-bitmap"
	StageData   = "data-block"
)

var (
	errMagic = errors.New("segment: bad magic")
	errVer   = errors.New("segment: unsupported version")
)

// CorruptError identifies where a segment failed validation.
type CorruptError struct {
	Group int    // -1 for the file header
	Stage string // header / stats / null-bitmap / data-block
	Err   error
}

func (e *CorruptError) Error() string {
	return fmt.Sprintf("segment corrupt: group=%d stage=%s: %v", e.Group, e.Stage, e.Err)
}

func (e *CorruptError) Unwrap() error { return e.Err }

// cursor is a bounds-checked byte reader; every take verifies length first.
type cursor struct {
	buf []byte
	pos int
}

func (c *cursor) need(n int) error {
	if n < 0 || c.pos+n > len(c.buf) {
		return bitpack.ErrShort
	}
	return nil
}

func (c *cursor) u8() (byte, error) {
	if err := c.need(1); err != nil {
		return 0, err
	}
	v := c.buf[c.pos]
	c.pos++
	return v, nil
}

func (c *cursor) u16() (uint16, error) {
	if err := c.need(2); err != nil {
		return 0, err
	}
	v := uint16(c.buf[c.pos])<<8 | uint16(c.buf[c.pos+1])
	c.pos += 2
	return v, nil
}

func (c *cursor) u32() (uint32, error) {
	if err := c.need(4); err != nil {
		return 0, err
	}
	v := uint32(c.buf[c.pos])<<24 | uint32(c.buf[c.pos+1])<<16 |
		uint32(c.buf[c.pos+2])<<8 | uint32(c.buf[c.pos+3])
	c.pos += 4
	return v, nil
}

func (c *cursor) u64() (uint64, error) {
	if err := c.need(8); err != nil {
		return 0, err
	}
	var v uint64
	for i := 0; i < 8; i++ {
		v = v<<8 | uint64(c.buf[c.pos+i])
	}
	c.pos += 8
	return v, nil
}

func (c *cursor) i64() (int64, error) {
	v, err := c.u64()
	return int64(v), err
}

func (c *cursor) bytes(n int) ([]byte, error) {
	if err := c.need(n); err != nil {
		return nil, err
	}
	v := c.buf[c.pos : c.pos+n]
	c.pos += n
	return v, nil
}

func (c *cursor) string() (string, error) {
	n, err := c.u32()
	if err != nil {
		return "", err
	}
	b, err := c.bytes(int(n))
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// ---- little encoders used while building a group block ----

func putU8(b *[]byte, v byte) { *b = append(*b, v) }

func putU32(b *[]byte, v uint32) {
	*b = append(*b, byte(v>>24), byte(v>>16), byte(v>>8), byte(v))
}

func putI64(b *[]byte, v int64) {
	u := uint64(v)
	for i := 7; i >= 0; i-- {
		*b = append(*b, byte(u>>(uint(i)*8)))
	}
}

func putStr(b *[]byte, v string) {
	putU32(b, uint32(len(v)))
	*b = append(*b, v...)
}

func nullBitmap(vals []zone.Value) []byte {
	bm := make([]byte, (len(vals)+7)/8)
	for i, v := range vals {
		if v.Kind != zone.KindNull {
			bm[i>>3] |= 1 << (uint(i) & 7)
		}
	}
	return bm
}

func presentValues(vals []zone.Value) []zone.Value {
	out := make([]zone.Value, 0, len(vals))
	for _, v := range vals {
		if v.Kind != zone.KindNull {
			out = append(out, v)
		}
	}
	return out
}
