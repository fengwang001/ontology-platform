// Package dict assigns stable, densely packed codes to distinct values.
package dict

import "errors"

var ErrInvalidCode = errors.New("dict: code is outside dictionary")

// Encoder is a value-to-code dictionary. Codes are assigned in first-seen
// order and are stable for the life of the encoder.
type Encoder[T comparable] struct {
	values []T
	index  map[T]uint64
}

func New[T comparable]() *Encoder[T] {
	return &Encoder[T]{index: make(map[T]uint64)}
}

// Add returns the code for value, assigning a new code on first appearance.
func (e *Encoder[T]) Add(value T) uint64 {
	if code, ok := e.index[value]; ok {
		return code
	}
	code := uint64(len(e.values))
	e.values = append(e.values, value)
	e.index[value] = code
	return code
}

// Encode returns one code per input value.
func (e *Encoder[T]) Encode(values []T) []uint64 {
	codes := make([]uint64, len(values))
	for i, value := range values {
		codes[i] = e.Add(value)
	}
	return codes
}

// Cardinality is the number of distinct values.
func (e *Encoder[T]) Cardinality() int { return len(e.values) }

// Values returns the immutable-looking reverse dictionary in code order.
func (e *Encoder[T]) Values() []T {
	out := make([]T, len(e.values))
	copy(out, e.values)
	return out
}

// Lookup maps a code back to its value.
func (e *Encoder[T]) Lookup(code uint64) (T, error) {
	var zero T
	if int(code) >= len(e.values) {
		return zero, ErrInvalidCode
	}
	return e.values[code], nil
}

// Decode maps every code back to its value.
func (e *Encoder[T]) Decode(codes []uint64) ([]T, error) {
	out := make([]T, len(codes))
	for i, code := range codes {
		value, err := e.Lookup(code)
		if err != nil {
			return nil, err
		}
		out[i] = value
	}
	return out, nil
}

// Width is the minimum bit width that can represent all assigned codes.
func (e *Encoder[T]) Width() int {
	n := uint64(len(e.values))
	switch {
	case n <= 1:
		return 1
	case n > uint64(1)<<63:
		return 64
	default:
		width := 0
		for v := n - 1; v != 0; v >>= 1 {
			width++
		}
		return width
	}
}
