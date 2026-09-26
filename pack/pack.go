// Package pack adds schema management and mutex-guarded bitmaps with
// O(1) bit location on top of package bits.
package pack

import (
	"errors"
	"sync"

	"ontology/bits"
)

// ErrArity: Pack called with a value count different from the schema's.
var ErrArity = errors.New("pack: value count does not match schema")

// Schema describes a fixed sequence of bitfields.
type Schema struct {
	widths []int
	signed []bool
	total  int
}

// NewSchema validates widths (each in [1,64], total <= 64).
func NewSchema(widths []int, signed []bool) (Schema, error) {
	if len(widths) != len(signed) {
		return Schema{}, ErrArity
	}
	s := Schema{widths: append([]int(nil), widths...), signed: append([]bool(nil), signed...)}
	for _, w := range widths {
		if w < 1 || w > 64 {
			return Schema{}, bits.ErrBadWidth
		}
		s.total += w
	}
	if s.total > 64 {
		return Schema{}, bits.ErrBadWidth
	}
	return s, nil
}

// Pack packs one value per schema field, least-significant-bits first.
func (s Schema) Pack(values ...int64) (uint64, error) {
	if len(values) != len(s.widths) {
		return 0, ErrArity
	}
	fs := make([]bits.Field, len(values))
	for i, v := range values {
		fs[i] = bits.Field{Width: s.widths[i], Value: v, Signed: s.signed[i]}
	}
	return bits.PackFields(fs)
}

// Unpack extracts every field from word in schema order.
func (s Schema) Unpack(word uint64) ([]int64, error) {
	if s.total == 0 || s.total > 64 {
		return nil, bits.ErrBadWidth
	}
	out := make([]int64, len(s.widths))
	off := 0
	for i, w := range s.widths {
		out[i] = bits.Extract(word, off, w, s.signed[i])
		off += w
	}
	return out, nil
}

// Bitmap is a fixed-size bitmap safe for concurrent use. Bit location is
// O(1): byte idx/8, mask 1<<(idx%8); probed records how many bits the last
// locate actually inspected (always 1 here — no scanning).
type Bitmap struct {
	mu     sync.Mutex
	buf    []byte
	probed int
}

// NewBitmap returns a bitmap holding nbits bits, rounded up to whole bytes.
func NewBitmap(nbits int) *Bitmap {
	if nbits < 0 {
		nbits = 0
	}
	return &Bitmap{buf: make([]byte, (nbits+7)/8)}
}

// locate resolves idx to its byte and mask. It inspects exactly one bit
// position, so probed becomes 1 on success and stays untouched on failure.
func (b *Bitmap) locate(idx int) (byteOff int, mask byte, ok bool) {
	if idx < 0 || idx >= 8*len(b.buf) {
		return 0, 0, false
	}
	b.probed = 1
	return idx / 8, 1 << (idx % 8), true
}

// Set sets bit idx; out-of-range idx fails with bits.ErrBitIndex.
func (b *Bitmap) Set(idx int) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	off, m, ok := b.locate(idx)
	if !ok {
		return bits.ErrBitIndex
	}
	b.buf[off] |= m
	return nil
}

// Clear clears bit idx; out-of-range idx fails with bits.ErrBitIndex.
func (b *Bitmap) Clear(idx int) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	off, m, ok := b.locate(idx)
	if !ok {
		return bits.ErrBitIndex
	}
	b.buf[off] &^= m
	return nil
}

// Test reports whether bit idx is set.
func (b *Bitmap) Test(idx int) (bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	off, m, ok := b.locate(idx)
	if !ok {
		return false, bits.ErrBitIndex
	}
	return b.buf[off]&m != 0, nil
}
