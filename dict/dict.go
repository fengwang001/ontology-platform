// Package dict holds the bounded value→code dictionary used by the encoder.
// It has no dependencies on the other packages in this module.
package dict

import (
	"errors"
	"strconv"
)

// Internal self-check failures; the probe count itself never leaves the package.
var (
	errLookupMiss  = errors.New("dict: expected lookup to miss")
	errProbeGrowth = errors.New("dict: probe count grew with dictionary size")
)

// key builds a distinct non-empty value for the i-th self-check entry.
func key(i int) string { return "v" + strconv.Itoa(i) }

// Dict is a bounded dictionary. Codes are allocated in first-seen order from
// 0. Reset clears both views; the capacity never changes.
type Dict struct {
	k       int
	forward map[string]int
	inverse map[int]string
	// probes records how many entries were inspected by the most recent
	// hit/miss lookup. Unexported by design: callers must not observe its value.
	probes int
}

// New creates a dictionary with capacity k. New requires k > 0; the api
// package is responsible for rejecting invalid configuration before this call.
func New(k int) *Dict {
	return &Dict{
		k:       k,
		forward: make(map[string]int),
		inverse: make(map[int]string),
	}
}

// Lookup reports whether value is in the dictionary and its code. A hash-map
// lookup inspects one slot, so probes is O(1) regardless of dictionary size.
func (d *Dict) Lookup(value string) (int, bool) {
	d.probes = 1
	code, ok := d.forward[value]
	return code, ok
}

// Full reports whether the dictionary already holds k distinct values.
func (d *Dict) Full() bool { return len(d.forward) == d.k }

// Put registers a new value, allocating code = current size, and returns it.
func (d *Dict) Put(value string) int {
	code := len(d.forward)
	d.forward[value] = code
	d.inverse[code] = value
	return code
}

// Set installs an explicit code→value binding while decoding a put token.
func (d *Dict) Set(code int, value string) {
	d.inverse[code] = value
}

// At returns the value bound to code on the decoding side.
func (d *Dict) At(code int) (string, bool) {
	v, ok := d.inverse[code]
	return v, ok
}

// Reset empties both views. Codes restart at 0 after a reset.
func (d *Dict) Reset() {
	d.forward = make(map[string]int)
	d.inverse = make(map[int]string)
}

// Cap returns the fixed capacity.
func (d *Dict) Cap() int { return d.k }

// Size returns the number of distinct values currently held.
func (d *Dict) Size() int { return len(d.forward) }

// SelfCheck verifies, across several dictionary sizes, that the number of
// entries probed by a lookup does not grow with the dictionary size. It
// exposes only pass/fail, never the counter value.
func SelfCheck() error {
	sizes := []int{100, 1000, 5000, 10000}
	first := -1
	for _, m := range sizes {
		q := New(m + 1)
		for i := 0; i < m; i++ {
			q.Put(key(i))
		}
		if _, ok := q.Lookup(key(m)); ok {
			return errLookupMiss
		}
		got := q.probes
		if first == -1 {
			first = got
		}
		if got != first {
			return errProbeGrowth
		}
	}
	return nil
}
