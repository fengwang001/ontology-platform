// Package dict implements dictionary encoding: deduplicate values, assign
// dense code words and map codes back to values. It depends on no other
// package in this module.
package dict

import "errors"

// ErrCode is returned when a code word is out of the dictionary range.
var ErrCode = errors.New("dict: code out of range")

// Int is a dictionary over int64 values.
type Int struct {
	values []int64
	index  map[int64]uint64
}

// NewInt builds a dictionary. Codes are assigned in first-seen order so the
// mapping is deterministic.
func NewInt(vals []int64) *Int {
	d := &Int{index: make(map[int64]uint64)}
	for _, v := range vals {
		if _, ok := d.index[v]; !ok {
			d.index[v] = uint64(len(d.values))
			d.values = append(d.values, v)
		}
	}
	return d
}

// Cardinality reports the number of distinct values.
func (d *Int) Cardinality() int { return len(d.values) }

// Values returns the distinct values in code order.
func (d *Int) Values() []int64 { return d.values }

// Code returns the code word for v.
func (d *Int) Code(v int64) uint64 { return d.index[v] }

// Lookup returns the value behind a code word.
func (d *Int) Lookup(code uint64) (int64, error) {
	if code >= uint64(len(d.values)) {
		return 0, ErrCode
	}
	return d.values[code], nil
}

// Encode replaces each value with its code word.
func (d *Int) Encode(vals []int64) []uint64 {
	codes := make([]uint64, len(vals))
	for i, v := range vals {
		codes[i] = d.index[v]
	}
	return codes
}

// Decode maps code words back to values.
func (d *Int) Decode(codes []uint64) ([]int64, error) {
	out := make([]int64, len(codes))
	for i, c := range codes {
		v, err := d.Lookup(c)
		if err != nil {
			return nil, err
		}
		out[i] = v
	}
	return out, nil
}

// Str is a dictionary over string values; empty string is a valid member.
type Str struct {
	values []string
	index  map[string]uint64
}

// NewStr builds a string dictionary in first-seen order.
func NewStr(vals []string) *Str {
	d := &Str{index: make(map[string]uint64)}
	for _, v := range vals {
		if _, ok := d.index[v]; !ok {
			d.index[v] = uint64(len(d.values))
			d.values = append(d.values, v)
		}
	}
	return d
}

// Cardinality reports the number of distinct strings.
func (d *Str) Cardinality() int { return len(d.values) }

// Values returns the distinct strings in code order.
func (d *Str) Values() []string { return d.values }

// Code returns the code word for s.
func (d *Str) Code(s string) uint64 { return d.index[s] }

// Lookup returns the string behind a code word.
func (d *Str) Lookup(code uint64) (string, error) {
	if code >= uint64(len(d.values)) {
		return "", ErrCode
	}
	return d.values[code], nil
}

// Encode replaces each string with its code word.
func (d *Str) Encode(vals []string) []uint64 {
	codes := make([]uint64, len(vals))
	for i, v := range vals {
		codes[i] = d.index[v]
	}
	return codes
}

// Decode maps code words back to strings.
func (d *Str) Decode(codes []uint64) ([]string, error) {
	out := make([]string, len(codes))
	for i, c := range codes {
		s, err := d.Lookup(c)
		if err != nil {
			return nil, err
		}
		out[i] = s
	}
	return out, nil
}
