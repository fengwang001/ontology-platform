// Package api is the public entry point: build a suffix/LCP index from a byte
// string and query it. It depends on package lcp, which depends on suffix.
package api

import (
	"errors"
	"fmt"
	"slices"
	"sync"
	"unicode/utf8"

	"ontology/lcp"
	"ontology/suffix"
)

// The four errors are mutually distinct and matchable via errors.Is.
var (
	ErrEmpty       = errors.New("api: input must not be empty")
	ErrInvalidUTF8 = errors.New("api: input contains invalid UTF-8")
	ErrNotBuilt    = errors.New("api: index has not been built")
	ErrOutOfRange  = errors.New("api: suffix-array index out of range")
)

// Index is built via New; immutable afterwards, safe for concurrent readers.
type Index struct {
	mu    sync.RWMutex
	tab   *lcp.Table
	built bool
}

// New builds the index from s. Rejection leaves x completely untouched.
func (x *Index) New(s []byte) error {
	if len(s) == 0 {
		return ErrEmpty
	}
	if !utf8.Valid(s) {
		return ErrInvalidUTF8
	}
	cp := append([]byte(nil), s...) // callers cannot mutate internal state
	x.mu.Lock()
	x.tab, x.built = lcp.Build(cp, suffix.Build(cp)), true
	x.mu.Unlock()
	return nil
}
func (x *Index) ready() (*lcp.Table, error) {
	x.mu.RLock()
	defer x.mu.RUnlock()
	if !x.built {
		return nil, ErrNotBuilt
	}
	return x.tab, nil
}
func (x *Index) SA() ([]int, error) {
	t, err := x.ready()
	if err != nil {
		return nil, err
	}
	return t.SA(), nil
}
func (x *Index) LCP() ([]int, error) {
	t, err := x.ready()
	if err != nil {
		return nil, err
	}
	return t.LCP(), nil
}
func (x *Index) At(k int) (int, error) {
	t, err := x.ready()
	if err != nil {
		return 0, err
	}
	sa := t.SA()
	if k < 0 || k >= len(sa) {
		return 0, ErrOutOfRange
	}
	return sa[k], nil
}

// LongestRepeated returns the leftmost start and length of the longest
// substring occurring at least twice.
func (x *Index) LongestRepeated() (start, length int, err error) {
	t, e := x.ready()
	if e != nil {
		return 0, 0, e
	}
	st, ln := t.Longest()
	return st, ln, nil
}

// SelfCheck verifies the four invariants on built-in strings with hand-derived
// expected values and exercises all four rejection paths.
func (x *Index) SelfCheck() error {
	var z Index
	if err := z.New([]byte("ababab")); err != nil {
		return err
	}
	sa, _ := z.SA()
	lv, _ := z.LCP()
	st, ln, _ := z.LongestRepeated()
	wantSA := []int{4, 2, 0, 5, 3, 1}
	if !slices.Equal(sa, wantSA) { // invariant 1; permutation pinned by tests
		return errors.New("selfcheck: SA of ababab wrong")
	}
	if !slices.Equal(lv, []int{2, 4, 0, 1, 3}) { // invariant 2
		return errors.New("selfcheck: LCP of ababab wrong")
	}
	if st != 0 || ln != 4 { // invariant 3
		return fmt.Errorf("selfcheck: longest of ababab wrong: %d,%d", st, ln)
	}
	const m = 200 // worst shape: m equal bytes; SA[k]=m-1-k proves permutation, LCP[k]=k+1
	var w Index
	if err := w.New(slices.Repeat([]byte{'a'}, m)); err != nil {
		return err
	}
	wsa, _ := w.SA()
	wlc, _ := w.LCP()
	for k := 0; k < m; k++ {
		if wsa[k] != m-1-k || k < m-1 && wlc[k] != k+1 {
			return errors.New("selfcheck: all-a structure wrong")
		}
	}
	if ws, wl, _ := w.LongestRepeated(); ws != 0 || wl != m-1 {
		return errors.New("selfcheck: all-a longest wrong")
	}
	var q Index // invariant 4: four distinct rejections, no trace left
	if _, err := q.SA(); !errors.Is(err, ErrNotBuilt) {
		return err
	}
	if err := q.New(nil); !errors.Is(err, ErrEmpty) {
		return err
	}
	if err := q.New([]byte{0xff, 'a'}); !errors.Is(err, ErrInvalidUTF8) {
		return err
	}
	if _, err := q.LCP(); !errors.Is(err, ErrNotBuilt) {
		return err
	}
	if err := q.New([]byte("ababab")); err != nil {
		return err
	}
	for _, k := range []int{6, -1} {
		if _, err := q.At(k); !errors.Is(err, ErrOutOfRange) {
			return err
		}
	}
	if r, err := q.SA(); err != nil || !slices.Equal(r, wantSA) {
		return errors.New("selfcheck: state changed after rejection")
	}
	return nil
}
