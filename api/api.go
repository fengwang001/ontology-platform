// Package api is the public entry point for longest common substring
// queries against a fixed in-memory reference string. All state lives in
// the process; only the standard library is used.
package api

import (
	"errors"
	"strings"

	"ontology/dp"
	"ontology/query"
)

// maxLen bounds len(a)+len(b) for every accepted operation.
const maxLen = 1 << 20

// Sentinel errors are mutually distinct and decidable with errors.Is.
var (
	ErrEmptyReference = errors.New("api: reference string must not be empty")
	ErrEmptyQuery     = errors.New("api: query string must not be empty")
	ErrInputTooLong   = errors.New("api: len(a)+len(b) exceeds maxLen")
	ErrSelfCheck      = errors.New("api: self-check failed")
)

// Matcher holds the fixed reference string a.
type Matcher struct {
	a string
	q *query.Query
}

// New creates a Matcher for ref. A rejected New changes no state.
func New(ref string) (*Matcher, error) {
	if ref == "" {
		return nil, ErrEmptyReference
	}
	if len(ref) > maxLen {
		return nil, ErrInputTooLong
	}
	return &Matcher{a: ref, q: query.New(ref)}, nil
}

func (m *Matcher) validate(b string) error {
	if b == "" {
		return ErrEmptyQuery
	}
	if len(m.a)+len(b) > maxLen {
		return ErrInputTooLong
	}
	return nil
}

// Query returns the longest common substring length and its 0-based start
// in a. Validation happens before any state is touched.
func (m *Matcher) Query(b string) (length, start int, err error) {
	if err = m.validate(b); err != nil {
		return 0, 0, err
	}
	r := m.q.Run(b)
	return r.Length, r.Start, nil
}

// Substring returns the longest common substring itself.
func (m *Matcher) Substring(b string) (string, error) {
	l, st, err := m.Query(b)
	if err != nil {
		return "", err
	}
	return m.a[st : st+l], nil
}

// naive enumerates all start pairs; it is the SelfCheck oracle.
func naive(a, b string) (length, start int) {
	best, bestStart := 0, 0
	for i := 0; i < len(a); i++ {
		for j := 0; j < len(b); j++ {
			k := 0
			for i+k < len(a) && j+k < len(b) && a[i+k] == b[j+k] {
				k++
			}
			if k > best {
				best, bestStart = k, i
			}
		}
	}
	return best, bestStart
}

// SelfCheck verifies the four invariants on built-in pairs: rolling/full
// table/naive agreement, substring truth, and that rejected operations
// leave observable state unchanged.
func (m *Matcher) SelfCheck() error {
	if err := dp.SelfCheck(); err != nil {
		return ErrSelfCheck
	}

	pairs := [][2]string{
		{"banana", "ananas"},
		{"abcx", "abc"},
		{"abcde", "abfce"},
		{m.a, m.a},
	}
	for _, p := range pairs {
		r := query.New(p[0]).Run(p[1])
		wl, ws := naive(p[0], p[1])
		if r.Length != wl || r.Start != ws || r.Text != p[0][ws:ws+wl] {
			return ErrSelfCheck
		}
	}

	// Rejected operations on a fresh instance must leave it usable.
	tmp, err := New("banana")
	if err != nil {
		return ErrSelfCheck
	}
	if _, _, e := tmp.Query(""); e != ErrEmptyQuery {
		return ErrSelfCheck
	}
	if _, _, e := tmp.Query(strings.Repeat("x", maxLen)); e != ErrInputTooLong {
		return ErrSelfCheck
	}
	if l, st, e := tmp.Query("ananas"); e != nil || l != 5 || st != 1 {
		return ErrSelfCheck
	}
	return nil
}
