// Package dict implements dictionary encoding: deduplication, code
// assignment and inverse mapping for any comparable value type.
package dict

import (
	"errors"
	"io"
)

// ErrCode is returned when an inverse mapping uses an unknown code.
var ErrCode = errors.New("dict: unknown code")

// Dict maps distinct values of type T to dense integer codes and back.
type Dict[T comparable] struct {
	values []T
	index  map[T]uint32
}

// Build assigns codes 0..k-1 to distinct values in first-seen order.
func Build[T comparable](values []T) (*Dict[T], []uint32) {
	d := &Dict[T]{index: make(map[T]uint32)}
	codes := make([]uint32, len(values))
	for i, v := range values {
		c, ok := d.index[v]
		if !ok {
			c = uint32(len(d.values))
			d.index[v] = c
			d.values = append(d.values, v)
		}
		codes[i] = c
	}
	return d, codes
}

// Len returns the number of distinct values.
func (d *Dict[T]) Len() int { return len(d.values) }

// Code returns the code assigned to v.
func (d[T]) Code(v T) (uint32, bool) {
	c, ok := d.index[v]
	return c, ok
}

// Lookup returns the value assigned to code c.
func (d *Dict[T]) Lookup(c uint32) (T, bool) {
	var zero T
	if int(c) >= len(d.values) {
		return zero, false
	}
	return d.values[c], true
}

// Values returns all dictionary entries in code order.
func (d *Dict[T]) Values() []T {
	out := make([]T, len(d.values))
	copy(out, d.values)
	return out
}

// WriteTo serializes the dictionary without using encoding/gob or json.
// enc writes one value; the caller owns the concrete type encoding.
func (d *Dict[T]) WriteTo(w io.Writer, enc func(io.Writer, T) error) (int64, error) {
	cw := &countWriter{w: w}
	if err := WriteUvarint(cw, uint64(len(d.values))); err != nil {
		return cw.n, err
	}
	for _, v := range d.values {
		if err := enc(cw, v); err != nil {
			return cw.n, err
		}
	}
	return cw.n, nil
}

// ReadFrom rebuilds a dictionary serialized by WriteTo.
func ReadFrom[T comparable](r io.Reader, dec func(io.Reader) (T, error)) (*Dict[T], error) {
	n, err := ReadUvarint(r)
	if err != nil {
		return nil, err
	}
	d := &Dict[T]{values: make([]T, 0, n), index: make(map[T]uint32, n)}
	for i := uint64(0); i < n; i++ {
		v, err := dec(r)
		if err != nil {
			return nil, err
		}
		if _, dup := d.index[v]; dup {
			return nil, errors.New("dict: duplicate entry in stream")
		}
		d.index[v] = uint32(len(d.values))
		d.values = append(d.values, v)
	}
	return d, nil
}

type countWriter struct {
	w io.Writer
	n int64
}

func (c *countWriter) Write(p []byte) (int, error) {
	k, err := c.w.Write(p)
	c.n += int64(k)
	return k, err
}
