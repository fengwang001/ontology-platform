// Package dict provides dictionary encoding: deduplicate a column's
// distinct values into a table and represent each occurrence by a dense
// integer code. Separate builders exist for int64 and byte-string values.
package dict

import "errors"

// ErrCorrupt marks malformed serialized dictionaries or code streams.
var ErrCorrupt = errors.New("dict: corrupt encoding")

// Width returns the minimal bit width for code values in [0, cardinal-1].
// A zero-cardinality dictionary has width 0.
func Width(cardinal int) uint {
	if cardinal <= 1 {
		return 0
	}
	w := uint(1)
	for (uint64(1) << w) < uint64(cardinal) {
		w++
	}
	return w
}

// IntBuilder maps int64 values to dense codes in first-seen order.
type IntBuilder struct {
	index map[int64]int
	vals  []int64
}

// NewIntBuilder returns an empty int64 dictionary builder.
func NewIntBuilder() *IntBuilder {
	return &IntBuilder{index: make(map[int64]int)}
}

// Add inserts v if unseen and returns its code.
func (b *IntBuilder) Add(v int64) int {
	if c, ok := b.index[v]; ok {
		return c
	}
	c := len(b.vals)
	b.index[v] = c
	b.vals = append(b.vals, v)
	return c
}

// Cardinal reports the number of distinct values.
func (b *IntBuilder) Cardinal() int { return len(b.vals) }

// Values returns the code-to-value table in code order.
func (b *IntBuilder) Values() []int64 { return b.vals }

// CodeOf returns the code for an existing value and false if absent.
func (b *IntBuilder) CodeOf(v int64) (int, bool) {
	c, ok := b.index[v]
	return c, ok
}

// BytesBuilder maps []byte values (semantically strings) to dense codes.
type BytesBuilder struct {
	index map[string]int
	vals  [][]byte
}

// NewBytesBuilder returns an empty byte-string dictionary builder.
func NewBytesBuilder() *BytesBuilder {
	return &BytesBuilder{index: make(map[string]int)}
}

// Add inserts v if unseen and returns its code.
func (b *BytesBuilder) Add(v []byte) int {
	key := string(v)
	if c, ok := b.index[key]; ok {
		return c
	}
	c := len(b.vals)
	b.index[key] = c
	b.vals = append(b.vals, append([]byte(nil), v...))
	return c
}

// Cardinal reports the number of distinct values.
func (b *BytesBuilder) Cardinal() int { return len(b.vals) }

// Values returns the code-to-value table in code order.
func (b *BytesBuilder) Values() [][]byte { return b.vals }

// CodeOf returns the code for an existing value and false if absent.
func (b *BytesBuilder) CodeOf(v []byte) (int, bool) {
	c, ok := b.index[string(v)]
	return c, ok
}
