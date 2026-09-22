// Package dict implements dictionary encoding: deduplicate a stream of
// comparable values, assign dense codes in first-seen order, and provide the
// reverse mapping from code back to value.
package dict

import "errors"

// ErrTooMany is reported by Build when the number of distinct values exceeds
// the configured cardinality limit. Callers may use it to trigger fallback
// encodings.
var ErrTooMany = errors.New("dict: distinct value count exceeds limit")

// Dict is an immutable bidirectional mapping between values of type T and
// dense codes starting at zero. T must be comparable.
type Dict[T comparable] struct {
	values []T
	index  map[T]uint64
}

// Build constructs a dictionary from vals, preserving first-seen order.
// maxCard <= 0 means unlimited. When the distinct count exceeds maxCard it
// returns ErrTooMany without retaining partial state.
func Build[T comparable](vals []T, maxCard int) (*Dict[T], error) {
	d := &Dict[T]{index: make(map[T]uint64)}
	for _, v := range vals {
		if _, ok := d.index[v]; ok {
			continue
		}
		if maxCard > 0 && len(d.values) >= maxCard {
			return nil, ErrTooMany
		}
		d.index[v] = uint64(len(d.values))
		d.values = append(d.values, v)
	}
	return d, nil
}

// Code returns the code for v and whether v is present in the dictionary.
func (d *Dict[T]) Code(v T) (uint64, bool) {
	c, ok := d.index[v]
	return c, ok
}

// MustCode is like Code but panics when v is absent; callers must only use it
// on values drawn from the stream the dictionary was built from.
func (d *Dict[T]) MustCode(v T) uint64 {
	c, ok := d.index[v]
	if !ok {
		panic("dict: value not in dictionary")
	}
	return c
}

// Value returns the value for code c. It panics if c is out of range.
func (d *Dict[T]) Value(c uint64) T {
	if int(c) >= len(d.values) {
		panic("dict: code out of range")
	}
	return d.values[c]
}

// Len returns the dictionary cardinality.
func (d *Dict[T]) Len() int { return len(d.values) }

// Encode maps vals to codes using the dictionary.
func Encode[T comparable](d *Dict[T], vals []T) []uint64 {
	out := make([]uint64, len(vals))
	for i, v := range vals {
		out[i] = d.MustCode(v)
	}
	return out
}

// Decode maps codes back to values.
func Decode[T comparable](d *Dict[T], codes []uint64) []T {
	out := make([]T, len(codes))
	for i, c := range codes {
		out[i] = d.Value(c)
	}
	return out
}
