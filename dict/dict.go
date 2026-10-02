// Package dict builds dictionaries for dictionary encoding: distinct
// values are numbered with codes in first-appearance order, and the
// immutable Dict maps codes back to values.
package dict

import "errors"

// ErrCardinality is returned by Builder.Add when a new distinct value
// would exceed the configured maximum cardinality. The builder state
// is unchanged when this error is returned.
var ErrCardinality = errors.New("dict: max cardinality exceeded")

// Builder assigns codes to distinct values up to a maximum cardinality.
type Builder struct {
	max  int
	idx  map[int64]uint32
	vals []int64
}

// NewBuilder returns a Builder allowing at most max distinct values.
func NewBuilder(max int) *Builder {
	return &Builder{max: max, idx: make(map[int64]uint32)}
}

// Add returns the code for v, assigning a new one on first sight.
// It returns ErrCardinality (state unchanged) when a new value would
// exceed the maximum cardinality.
func (b *Builder) Add(v int64) (uint32, error) {
	if c, ok := b.idx[v]; ok {
		return c, nil
	}
	if len(b.vals) >= b.max {
		return 0, ErrCardinality
	}
	c := uint32(len(b.vals))
	b.idx[v] = c
	b.vals = append(b.vals, v)
	return c, nil
}

// Len returns the current cardinality.
func (b *Builder) Len() int { return len(b.vals) }

// Dict finalizes the builder into an immutable reverse mapping.
func (b *Builder) Dict() *Dict {
	vals := make([]int64, len(b.vals))
	copy(vals, b.vals)
	return &Dict{vals: vals}
}

// Dict is an immutable code -> value mapping.
type Dict struct {
	vals []int64
}

// Len returns the number of dictionary entries.
func (d *Dict) Len() int { return len(d.vals) }

// Value returns the value for code. Callers must ensure code < Len();
// segment decoding validates dictionary size before lookup.
func (d *Dict) Value(code uint32) int64 { return d.vals[code] }

// Values returns a copy of the dictionary values in code order.
func (d *Dict) Values() []int64 {
	out := make([]int64, len(d.vals))
	copy(out, d.vals)
	return out
}
