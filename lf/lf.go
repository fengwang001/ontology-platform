// Package lf computes the first column F, occurrence counts, the LF
// mapping, and the inverse BWT.
package lf

import (
	"errors"
	"math/rand"

	"ontology/rot"
)

var (
	// ErrInvalidPrimary is returned when primary is out of range.
	ErrInvalidPrimary = errors.New("lf: primary index out of range")
	// ErrTerminatorCount is returned when term does not occur exactly once in last.
	ErrTerminatorCount = errors.New("lf: terminator count != 1")
)

// Table holds the precomputed LF-mapping data for one last column.
// A Table is built per Inverse call; it is not safe for concurrent use.
type Table struct {
	last    []byte
	f       []byte   // first column: sorted(last)
	less    [256]int // less[c] = number of bytes in last strictly < c
	occ     []int    // occ[i] = occurrences of last[i] within last[:i+1]
	checked int      // chars examined by the most recent LF call (unexported, diagnostic only)
}

// NewTable builds the LF table for last. It fails wholesale with
// ErrTerminatorCount unless term occurs exactly once in last.
func NewTable(last []byte, term byte) (*Table, error) {
	if rot.Count(last, term) != 1 {
		return nil, ErrTerminatorCount
	}
	t := &Table{last: last, f: rot.Sorted(last), occ: make([]int, len(last))}
	var total [256]int
	for _, c := range last {
		total[c]++
	}
	sum := 0
	for c := 0; c < 256; c++ {
		t.less[c] = sum
		sum += total[c]
	}
	var seen [256]int
	for i, c := range last {
		seen[c]++
		t.occ[i] = seen[c]
	}
	return t, nil
}

// LF returns the LF-mapping value for row i: the number of bytes
// lexicographically smaller than last[i], plus the rank of last[i]
// among last[:i+1] minus one. O(1) via the count tables; it examines
// exactly one character (last[i]) and records that in t.checked.
func (t *Table) LF(i int) int {
	c := t.last[i]
	t.checked = 1
	return t.less[c] + t.occ[i] - 1
}

// Inverse reconstructs s from the BWT last column and primary row by
// walking the LF mapping, then stripping the trailing terminator.
// Invalid input fails wholesale: no partial result, no state kept.
func Inverse(last []byte, primary int, term byte) ([]byte, error) {
	if primary < 0 || primary >= len(last) {
		return nil, ErrInvalidPrimary
	}
	tab, err := NewTable(last, term)
	if err != nil {
		return nil, err
	}
	n := len(last)
	out := make([]byte, n)
	row := primary
	for i := n - 1; i >= 0; i-- {
		out[i] = tab.last[row]
		row = tab.LF(row)
	}
	return out[:n-1], nil // strip trailing term
}

// MaxRankChecks transforms random strings of the given sizes, probes
// single LF lookups, and returns the maximum number of characters one
// LF call examined. Diagnostic helper; the counter itself stays
// unexported and out of every signature.
func MaxRankChecks(sizes ...int) int {
	rng := rand.New(rand.NewSource(1))
	max := 0
	for _, m := range sizes {
		s := make([]byte, m)
		for i := range s {
			s[i] = byte(rng.Intn(255)) + 1 // never 0x00
		}
		last, _, err := rot.Transform(s, 0x00)
		if err != nil {
			continue
		}
		tab, err := NewTable(last, 0x00)
		if err != nil {
			continue
		}
		for _, i := range []int{0, m / 2, m} {
			tab.LF(i)
			if tab.checked > max {
				max = tab.checked
			}
		}
	}
	return max
}
