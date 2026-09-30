// Package dict builds dictionary encodings: value deduplication, dense code
// word assignment and reverse lookup. It is storage-agnostic.
package dict

import "errors"

// ErrCardinality reports that distinct values exceeded the configured limit.
var ErrCardinality = errors.New("dict: cardinality over limit")

// Dict maps keys to dense code words in first-appearance order.
type Dict[K comparable] struct {
	keys []K
	idx  map[K]uint64
}

// Build deduplicates vals into a dictionary and returns the code word for
// every input position. limit<=0 means unbounded. When the number of
// distinct keys exceeds limit it returns ErrCardinality with no dictionary.
func Build[K comparable](vals []K, limit int) (*Dict[K], []uint64, error) {
	d := &Dict[K]{idx: make(map[K]uint64)}
	codes := make([]uint64, len(vals))
	for i, v := range vals {
		c, ok := d.idx[v]
		if !ok {
			if limit > 0 && len(d.keys) >= limit {
				return nil, nil, ErrCardinality
			}
			c = uint64(len(d.keys))
			d.keys = append(d.keys, v)
			d.idx[v] = c
		}
		codes[i] = c
	}
	return d, codes, nil
}

// FromKeys reconstructs a dictionary from an ordered key table.
func FromKeys[K comparable](keys []K) *Dict[K] {
	d := &Dict[K]{keys: append([]K(nil), keys...), idx: make(map[K]uint64, len(keys))}
	for i, k := range d.keys {
		d.idx[k] = uint64(i)
	}
	return d
}

// Len is the number of distinct keys.
func (d *Dict[K]) Len() int { return len(d.keys) }

// Code returns the code word for a key.
func (d *Dict[K]) Code(v K) (uint64, bool) {
	c, ok := d.idx[v]
	return c, ok
}

// Lookup maps a code word back to its key.
func (d *Dict[K]) Lookup(c uint64) (K, bool) {
	var zero K
	if int(c) >= len(d.keys) {
		return zero, false
	}
	return d.keys[c], true
}

// Keys returns the ordered key table as a copy.
func (d *Dict[K]) Keys() []K {
	out := make([]K, len(d.keys))
	copy(out, d.keys)
	return out
}
