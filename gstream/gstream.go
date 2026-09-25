// Package gstream packs Golomb-coded values into bytes and decodes them back.
// It depends only on ontology/golomb; bits are MSB-first, low bits padded.
package gstream

import (
	"errors"

	"ontology/golomb"
)

var (
	ErrInvalidParam = golomb.ErrInvalidParam
	ErrNegative     = golomb.ErrNegative
	ErrTruncated    = golomb.ErrTruncated
)

// Encode packs all values into one byte slice; empty input yields nil.
func Encode(M int, vs []int64) ([]byte, error) {
	c, err := golomb.New(M)
	if err != nil {
		return nil, err
	}
	w := &bitWriter{}
	for _, n := range vs {
		code, err := c.EncodeOne(n)
		if err != nil {
			return nil, err
		}
		for i := 0; i < len(code); i++ {
			w.writeBit(int(code[i] - '0'))
		}
	}
	return w.bytes(), nil
}

// Decode decodes a whole stream produced by Encode.
func Decode(M int, p []byte) ([]int64, error) {
	d, err := NewDecoder(M)
	if err != nil {
		return nil, err
	}
	d.Feed(p)
	return d.Decode()
}

type Decoder struct {
	codec    *golomb.Codec
	buf      []byte
	lastBits int // unexported: bits examined by the most recent DecodeOne
}

func NewDecoder(M int) (*Decoder, error) {
	c, err := golomb.New(M)
	if err != nil {
		return nil, err
	}
	return &Decoder{codec: c}, nil
}

// Feed appends another chunk of the byte stream, in any chunking.
func (d *Decoder) Feed(p []byte) {
	d.buf = append(d.buf, p...)
}

// Decode decodes fed bytes; on real truncation it returns nil without
// consuming them, so the caller may Feed more and retry.
func (d *Decoder) Decode() ([]int64, error) {
	r := bitReader{data: d.buf}
	total := 8 * len(d.buf)
	savedBits := d.lastBits
	var out []int64
	for {
		if total-r.bit < 8 && r.allZero(r.bit) { // <8 zero bits: padding
			return out, nil
		}
		mark := r.bit
		cr := &countingReader{inner: &r}
		v, err := d.codec.DecodeOne(cr)
		if err != nil {
			if errors.Is(err, golomb.ErrTruncated) && r.allZero(mark) {
				return out, nil // incomplete look was just zero padding
			}
			d.lastBits = savedBits // invariant 4: failure leaves no trace
			if errors.Is(err, golomb.ErrTruncated) {
				err = ErrTruncated
			}
			return nil, err
		}
		d.lastBits = cr.n
		out = append(out, v)
	}
}

// bitWriter packs bits MSB-first into bytes.
type bitWriter struct {
	buf []byte
	cur byte
	n   uint
}

func (w *bitWriter) writeBit(b int) {
	w.cur = (w.cur << 1) | byte(b&1)
	if w.n++; w.n == 8 {
		w.buf = append(w.buf, w.cur)
		w.cur, w.n = 0, 0
	}
}

func (w *bitWriter) bytes() []byte {
	if w.n == 0 {
		return w.buf
	}
	return append(append([]byte(nil), w.buf...), w.cur<<(8-w.n))
}

// bitReader is an MSB-first cursor over a byte slice.
type bitReader struct {
	data []byte
	bit  int
}

func (r *bitReader) ReadBit() (int, error) {
	if r.bit >= 8*len(r.data) {
		return 0, golomb.ErrTruncated
	}
	b := int((r.data[r.bit>>3] >> (7 - uint(r.bit&7))) & 1)
	r.bit++
	return b, nil
}

// allZero reports whether every bit at position >= from is zero.
func (r *bitReader) allZero(from int) bool {
	for i := from; i < 8*len(r.data); i++ {
		if r.data[i>>3]&(1<<(7-uint(i&7))) != 0 {
			return false
		}
	}
	return true
}

// countingReader wraps a BitReader and counts bits examined per code.
type countingReader struct {
	inner golomb.BitReader
	n     int
}

func (c *countingReader) ReadBit() (int, error) {
	c.n++
	return c.inner.ReadBit()
}
