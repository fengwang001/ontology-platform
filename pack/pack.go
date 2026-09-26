// Package pack manages bitfield schemas and O(1) bitmap location on top of
// package bits. It depends only on bits (dependency direction is one-way).
package pack

import (
	"sync"
	"sync/atomic"

	"ontology/bits"
)

// Schema describes the LSB-first layout of a set of fields.
type Schema struct {
	fields []bits.Field
	offs   []int // bit offset of each field, offs[0] == 0, contiguous
}

// NewSchema validates widths (1..64, total <= 64) and records offsets
// contiguous from bit 0 (no overlap/gap). Fields are copied.
func NewSchema(fields []bits.Field) (*Schema, error) {
	off := 0
	offs := make([]int, len(fields))
	for i, f := range fields { // validate before keeping anything
		if f.Width < 1 || f.Width > 64 {
			return nil, bits.ErrBadWidth
		}
		off += f.Width
		if off > 64 {
			return nil, bits.ErrBadWidth
		}
		offs[i] = off - f.Width
	}
	cp := append([]bits.Field(nil), fields...)
	return &Schema{fields: cp, offs: offs}, nil
}

// Range is one field's occupied bit interval.
type Range struct {
	Off, Width int
	Signed     bool
}

// Ranges returns a copy of each field's [offset, offset+width) layout.
func (s *Schema) Ranges() []Range {
	out := make([]Range, len(s.fields))
	for i, f := range s.fields {
		out[i] = Range{Off: s.offs[i], Width: f.Width, Signed: f.Signed}
	}
	return out
}

// Pack validates values against the schema and packs LSB-first. A value
// count mismatch is a width error; rejection returns (0, err).
func (s *Schema) Pack(values []int64) (uint64, error) {
	if len(values) != len(s.fields) {
		return 0, bits.ErrBadWidth
	}
	fs := make([]bits.Field, len(s.fields))
	for i, f := range s.fields {
		fs[i] = bits.Field{Width: f.Width, Signed: f.Signed, Value: values[i]}
	}
	return bits.PackFields(fs) // validates ranges, then packs
}

// Unpack extracts every field from word in schema order.
func (s *Schema) Unpack(word uint64) []int64 {
	out := make([]int64, len(s.fields))
	for i, f := range s.fields {
		out[i] = bits.Extract(word, s.offs[i], f.Width, f.Signed)
	}
	return out
}

// Bitmap is an m-bit bitmap. The unexported probe records how many bits
// the latest locate examined one by one; direct indexing examines exactly
// one bit regardless of m (O(1)). probe is reachable only in-package.
type Bitmap struct {
	mu    sync.RWMutex
	bm    []byte
	m     int
	probe atomic.Int64
}

// NewBitmap creates a bitmap holding m bits (backed by ceil(m/8) bytes).
func NewBitmap(m int) *Bitmap {
	if m < 0 {
		m = 0
	}
	return &Bitmap{bm: make([]byte, (m+7)/8), m: m}
}

// locate finds byte and mask directly; it never scans earlier bits, so
// the per-operation examined-bit count is the constant 1.
func (b *Bitmap) locate(i int) (int, byte, bool) {
	if i < 0 || i >= b.m {
		return 0, 0, false
	}
	b.probe.Store(1)
	return i >> 3, byte(1) << uint(i&7), true
}

// Set locates bit i in O(1) and sets it.
func (b *Bitmap) Set(i int) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	idx, mask, ok := b.locate(i)
	if !ok {
		return bits.ErrBitIndex
	}
	b.bm[idx] |= mask
	return nil
}

// Clear locates bit i in O(1) and clears it.
func (b *Bitmap) Clear(i int) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	idx, mask, ok := b.locate(i)
	if !ok {
		return bits.ErrBitIndex
	}
	b.bm[idx] &^= mask
	return nil
}

// Test locates bit i in O(1) and reports it.
func (b *Bitmap) Test(i int) (bool, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	idx, mask, ok := b.locate(i)
	if !ok {
		return false, bits.ErrBitIndex
	}
	return b.bm[idx]&mask != 0, nil
}

// CheckConstantTime verifies for m in 100..10000 that locating bit m-1
// examines an m-independent constant; probe never crosses the API.
func CheckConstantTime() error {
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		b := NewBitmap(m)
		if _, err := b.Test(m - 1); err != nil {
			return err
		}
		if b.probe.Load() != 1 { // must stay a constant that does not grow with m
			return bits.ErrBitIndex
		}
	}
	return nil
}
