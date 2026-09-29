// Package dict implements dictionary encoding for comparable value types.
package dict

import (
	"errors"

	"ontology/bitpack"
)

// ErrTooLarge is returned when distinct-value cardinality exceeds the limit.
var ErrTooLarge = errors.New("dict: cardinality exceeds limit")

// ErrEmpty is returned when encoding a zero-length value sequence.
var ErrEmpty = errors.New("dict: no values to encode")

// Table is a bijective mapping between distinct values and dense code words.
type Table[T comparable] struct {
	values []T
	index  map[T]uint64
}

// Build assigns code words in first-seen order. maxCard<=0 means unlimited.
func Build[T comparable](vals []T, maxCard int) (*Table[T], []uint64, error) {
	if len(vals) == 0 {
		return nil, nil, ErrEmpty
	}
	t := &Table[T]{index: make(map[T]uint64)}
	codes := make([]uint64, len(vals))
	for i, v := range vals {
		c, ok := t.index[v]
		if !ok {
			if maxCard > 0 && len(t.values) >= maxCard {
				return nil, nil, ErrTooLarge
			}
			c = uint64(len(t.values))
			t.values = append(t.values, v)
			t.index[v] = c
		}
		codes[i] = c
	}
	return t, codes, nil
}

// Len is the number of distinct values.
func (t *Table[T]) Len() int { return len(t.values) }

// Values returns the inverse mapping, ordered by code.
func (t *Table[T]) Values() []T { return t.values }

// CodeWidth is the bit width needed for code words (minimum 1).
func (t *Table[T]) CodeWidth() int {
	w := bitpack.Width64(uint64(len(t.values) - 1))
	if w < 1 {
		return 1
	}
	return w
}

// Lookup returns the value for a code word and whether it existed.
func (t *Table[T]) Lookup(c uint64) (T, bool) {
	var zero T
	if int(c) >= len(t.values) {
		return zero, false
	}
	return t.values[c], true
}

// EncodeInt64 builds a self-describing block: w u8 | card u32 | codes | table.
func EncodeInt64(vals []int64, maxCard int) ([]byte, error) {
	tab, codes, err := Build(vals, maxCard)
	if err != nil {
		return nil, err
	}
	w := tab.CodeWidth()
	out := []byte{byte(w)}
	out = appendU32(out, uint32(tab.Len()))
	out = append(out, bitpack.Pack(codes, w)...)
	for _, v := range tab.Values() {
		out = appendU64(out, bitpack.Zig(v))
	}
	return out, nil
}

// DecodeInt64N decodes expecting exactly n non-null values.
func DecodeInt64N(block []byte, n int) ([]int64, error) {
	r := newReader(block)
	w, err := r.byte_()
	if err != nil {
		return nil, err
	}
	card, err := r.u32()
	if err != nil {
		return nil, err
	}
	codeBytes := bitpack.PackedLen(n, int(w))
	raw, err := r.bytes(codeBytes)
	if err != nil {
		return nil, err
	}
	codes, err := bitpack.Unpack(raw, int(w), n)
	if err != nil {
		return nil, err
	}
	table := make([]int64, card)
	for i := range table {
		u, err := r.u64()
		if err != nil {
			return nil, err
		}
		table[i] = bitpack.Unzig(u)
	}
	if !r.exhausted() {
		return nil, errors.New("dict: trailing bytes")
	}
	out := make([]int64, n)
	for i, c := range codes {
		if int(c) >= len(table) {
			return nil, errors.New("dict: code out of range")
		}
		out[i] = table[c]
	}
	return out, nil
}

// EncodeString builds a string dictionary block.
func EncodeString(vals []string, maxCard int) ([]byte, error) {
	tab, codes, err := Build(vals, maxCard)
	if err != nil {
		return nil, err
	}
	w := tab.CodeWidth()
	out := []byte{byte(w)}
	out = appendU32(out, uint32(tab.Len()))
	out = append(out, bitpack.Pack(codes, w)...)
	for _, v := range tab.Values() {
		if len(v) > 0xffff {
			return nil, errors.New("dict: string too long")
		}
		out = appendU16(out, uint16(len(v)))
		out = append(out, v...)
	}
	return out, nil
}

// DecodeStringN decodes expecting exactly n non-null values.
func DecodeStringN(block []byte, n int) ([]string, error) {
	r := newReader(block)
	w, err := r.byte_()
	if err != nil {
		return nil, err
	}
	card, err := r.u32()
	if err != nil {
		return nil, err
	}
	raw, err := r.bytes(bitpack.PackedLen(n, int(w)))
	if err != nil {
		return nil, err
	}
	codes, err := bitpack.Unpack(raw, int(w), n)
	if err != nil {
		return nil, err
	}
	table := make([]string, card)
	for i := range table {
		l, err := r.u16()
		if err != nil {
			return nil, err
		}
		b, err := r.bytes(int(l))
		if err != nil {
			return nil, err
		}
		table[i] = string(b)
	}
	if !r.exhausted() {
		return nil, errors.New("dict: trailing bytes")
	}
	out := make([]string, n)
	for i, c := range codes {
		if int(c) >= len(table) {
			return nil, errors.New("dict: code out of range")
		}
		out[i] = table[c]
	}
	return out, nil
}

// ---- small bounds-checked cursor reader ----

type reader struct {
	b   []byte
	off int
}

func newReader(b []byte) *reader { return &reader{b: b} }

var errShort = errors.New("dict: short read")

func (r *reader) take(k int) ([]byte, error) {
	if k < 0 || len(r.b)-r.off < k {
		return nil, errShort
	}
	s := r.b[r.off : r.off+k]
	r.off += k
	return s, nil
}

func (r *reader) byte_() (byte, error) {
	s, err := r.take(1)
	if err != nil {
		return 0, err
	}
	return s[0], nil
}

func (r *reader) u16() (uint16, error) {
	s, err := r.take(2)
	if err != nil {
		return 0, err
	}
	return uint16(s[0])<<8 | uint16(s[1]), nil
}

func (r *reader) u32() (uint32, error) {
	s, err := r.take(4)
	if err != nil {
		return 0, err
	}
	return uint32(s[0])<<24 | uint32(s[1])<<16 | uint32(s[2])<<8 | uint32(s[3]), nil
}

func (r *reader) u64() (uint64, error) {
	s, err := r.take(8)
	if err != nil {
		return 0, err
	}
	var u uint64
	for _, x := range s {
		u = u<<8 | uint64(x)
	}
	return u, nil
}

func (r *reader) bytes(k int) ([]byte, error) { return r.take(k) }

func (r *reader) exhausted() bool { return r.off == len(r.b) }

func appendU16(b []byte, v uint16) []byte { return append(b, byte(v>>8), byte(v)) }

func appendU32(b []byte, v uint32) []byte {
	return append(b, byte(v>>24), byte(v>>16), byte(v>>8), byte(v))
}

func appendU64(b []byte, v uint64) []byte {
	for i := 7; i >= 0; i-- {
		b = append(b, byte(v>>(uint(i)*8)))
	}
	return b
}
