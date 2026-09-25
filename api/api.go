// Package api is the public facade: build once with New, then run
// concurrent read-only Equal/LCP queries and SelfCheck.
package api

import (
	"bytes"
	"errors"
	"fmt"
	"sync/atomic"

	"ontology/query"
	"ontology/rhash"
)

// Distinguishable sentinel errors for every rejected operation.
var (
	ErrEmpty    = errors.New("api: empty input")
	ErrRange    = errors.New("api: index out of range")
	ErrNotBuilt = errors.New("api: not built")
)

type state struct {
	q *query.Querier
	s []byte // immutable copy of the input
}

var cur atomic.Pointer[state]

// New builds prefix tables for s in O(n). Empty input is rejected with
// ErrEmpty and leaves any previous state untouched.
func New(s []byte) error {
	if len(s) == 0 {
		return ErrEmpty
	}
	cp := append([]byte(nil), s...)
	cur.Store(&state{q: query.New(rhash.New(cp)), s: cp})
	return nil
}

func load() (*state, error) {
	st := cur.Load()
	if st == nil {
		return nil, ErrNotBuilt
	}
	return st, nil
}

// Equal reports whether s[l1:r1) == s[l2:r2). Invalid ranges are
// rejected with ErrRange; calling before New yields ErrNotBuilt.
func Equal(l1, r1, l2, r2 int) (bool, error) {
	st, err := load()
	if err != nil {
		return false, err
	}
	n := len(st.s)
	if l1 < 0 || r1 < l1 || r1 > n || l2 < 0 || r2 < l2 || r2 > n {
		return false, ErrRange
	}
	return st.q.Equal(l1, r1, l2, r2), nil
}

// LCP returns the longest common prefix length of s[i:] and s[j:].
func LCP(i, j int) (int, error) {
	st, err := load()
	if err != nil {
		return 0, err
	}
	if i < 0 || j < 0 || i > len(st.s) || j > len(st.s) {
		return 0, ErrRange
	}
	l, _ := st.q.LCP(i, j)
	return l, nil
}

// SelfCheck verifies the four invariants on built-in samples:
// Equal matches bytes.Equal, substring hashes match a naive recompute,
// LCP matches byte-by-byte comparison, and rejected calls change nothing.
func SelfCheck() error {
	st, err := load()
	if err != nil {
		return err
	}
	n := len(st.s)
	t := rhash.New(st.s)
	step := max(n/16, 1)
	naiveHash := func(l, r int) (uint64, uint64) {
		h1, h2 := uint64(0), uint64(0)
		for _, c := range st.s[l:r] {
			h1 = (h1*rhash.B + uint64(c)) % rhash.M1
			h2 = (h2*rhash.B + uint64(c)) % rhash.M2
		}
		return h1, h2
	}
	for l := 0; l <= n; l += step {
		for r := l; r <= n; r += step {
			a1, a2 := t.Hash(l, r)
			b1, b2 := naiveHash(l, r)
			if a1 != b1 || a2 != b2 {
				return fmt.Errorf("hash mismatch at [%d,%d)", l, r)
			}
			for l2 := 0; l2+r-l <= n; l2 += step {
				got := st.q.Equal(l, r, l2, l2+r-l)
				if got != bytes.Equal(st.s[l:r], st.s[l2:l2+r-l]) {
					return fmt.Errorf("equal mismatch at [%d,%d) [%d,%d)", l, r, l2, l2+r-l)
				}
			}
		}
	}
	for i := 0; i <= n; i += step {
		for j := 0; j <= n; j += step {
			got, _ := st.q.LCP(i, j)
			want := 0
			for i+want < n && j+want < n && st.s[i+want] == st.s[j+want] {
				want++
			}
			if got != want {
				return fmt.Errorf("lcp mismatch at %d,%d", i, j)
			}
		}
	}
	if _, err := Equal(-1, 0, 0, 0); !errors.Is(err, ErrRange) {
		return errors.New("rejection not signaled")
	}
	if _, err := Equal(0, 1, 0, 1); err != nil {
		return errors.New("state changed after rejection")
	}
	return nil
}
