// Package dict implements dictionary encoding for int64 values:
// deduplication, code word assignment and the reverse mapping.
package dict

import "errors"

// ErrCardinality is returned by Encode when the number of distinct
// values exceeds the dictionary's configured maximum cardinality.
// The dictionary is left unchanged in that case.
var ErrCardinality = errors.New("dict: cardinality limit exceeded")

// Dict maps int64 values to dense code words in [0, Size).
type Dict struct {
	values []int64
	index  map[int64]uint32
	max    int
}

// New creates an empty dictionary that accepts at most maxCard
// distinct values. maxCard <= 0 means unlimited.
func New(maxCard int) *Dict {
	return &Dict{index: make(map[int64]uint32), max: maxCard}
}

// Size returns the number of distinct values in the dictionary.
func (d *Dict) Size() int {
	return len(d.values)
}

// Values returns the dictionary entries; code i maps to Values()[i].
func (d *Dict) Values() []int64 {
	return d.values
}

// Value returns the value for code c.
func (d *Dict) Value(c uint32) int64 {
	return d.values[c]
}

// Encode assigns codes to vals and returns them. If a new value
// would push the cardinality past the configured maximum, Encode
// returns ErrCardinality and the dictionary is left unchanged, so
// the caller can fall back to another encoding.
func (d *Dict) Encode(vals []int64) ([]uint32, error) {
	codes := make([]uint32, len(vals))
	pending := make(map[int64]uint32)
	var added []int64
	for i, v := range vals {
		c, ok := d.index[v]
		if !ok {
			c, ok = pending[v]
		}
		if !ok {
			n := len(d.values) + len(added)
			if d.max > 0 && n >= d.max {
				return nil, ErrCardinality
			}
			c = uint32(n)
			pending[v] = c
			added = append(added, v)
		}
		codes[i] = c
	}
	for _, v := range added {
		d.index[v] = uint32(len(d.values))
		d.values = append(d.values, v)
	}
	return codes, nil
}

// Decode maps codes back to values. Codes must be < Size.
func (d *Dict) Decode(codes []uint32) []int64 {
	out := make([]int64, len(codes))
	for i, c := range codes {
		out[i] = d.values[c]
	}
	return out
}

// Build constructs a dictionary from an explicit value list, as read
// back from a serialized segment. Codes index into vals directly.
func Build(vals []int64) *Dict {
	d := &Dict{values: vals, index: make(map[int64]uint32, len(vals))}
	for i, v := range vals {
		d.index[v] = uint32(i)
	}
	return d
}
