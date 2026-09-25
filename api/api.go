// Package api exposes the BWT transform, inverse, and a self-check.
package api

import (
	"bytes"
	"errors"
	"fmt"
	"sort"

	"ontology/lf"
	"ontology/rot"
)

// Sentinel errors, distinguishable via errors.Is.
var (
	ErrTerminatorInInput = rot.ErrTerminatorInInput
	ErrInvalidPrimary    = lf.ErrInvalidPrimary
	ErrTerminatorCount   = lf.ErrTerminatorCount
)

// Transform computes the BWT of s with terminator term.
func Transform(s []byte, term byte) (last []byte, primary int, err error) {
	return rot.Transform(s, term)
}

// Inverse reconstructs s from the BWT last column and primary row.
func Inverse(last []byte, primary int, term byte) ([]byte, error) {
	return lf.Inverse(last, primary, term)
}

var selfCheckInputs = []string{"", "a", "banana", "aaaa", "mississippi", "abracadabra", "the quick brown fox"}

// SelfCheck verifies the four invariants over a set of built-in strings.
func SelfCheck() error {
	const term = '$'
	for _, s := range selfCheckInputs {
		last, primary, err := Transform([]byte(s), term)
		if err != nil {
			return fmt.Errorf("selfcheck transform %q: %w", s, err)
		}
		// Invariant 1: matches an independent naive reference, both ways.
		nl, np, nf := naiveForward([]byte(s), term)
		if !bytes.Equal(last, nl) || primary != np {
			return fmt.Errorf("selfcheck %q: forward diverges from naive reference", s)
		}
		got, err := Inverse(last, primary, term)
		if err != nil {
			return fmt.Errorf("selfcheck inverse %q: %w", s, err)
		}
		if want := naiveInverse(nl, np, term); !bytes.Equal(got, want) {
			return fmt.Errorf("selfcheck %q: inverse diverges from naive reference", s)
		}
		// Invariant 2: roundtrip.
		if string(got) != s {
			return fmt.Errorf("selfcheck %q: roundtrip got %q", s, got)
		}
		// Invariant 3: last is a permutation of the first column F;
		// counts = s counts + one term.
		if !bytes.Equal(rot.Sorted(last), nf) {
			return fmt.Errorf("selfcheck %q: sort(last) != F", s)
		}
		var cnt [256]int
		for _, c := range last {
			cnt[c]++
		}
		for i := 0; i < len(s); i++ {
			cnt[s[i]]--
		}
		cnt[term]--
		if cnt != ([256]int{}) {
			return fmt.Errorf("selfcheck %q: last counts != s counts + one term", s)
		}
	}
	// Invariant 4: failures are wholesale and leave no trace.
	if l, _, err := Transform([]byte("a$a"), term); !errors.Is(err, ErrTerminatorInInput) || l != nil {
		return errors.New("selfcheck: terminator-in-input not rejected cleanly")
	}
	if _, err := Inverse([]byte("annb$aa"), 7, term); !errors.Is(err, ErrInvalidPrimary) {
		return errors.New("selfcheck: invalid primary not rejected")
	}
	if _, err := Inverse([]byte("annbbaa"), 0, term); !errors.Is(err, ErrTerminatorCount) {
		return errors.New("selfcheck: bad terminator count not rejected")
	}
	if got, err := Inverse([]byte("annb$aa"), 4, term); err != nil || string(got) != "banana" {
		return errors.New("selfcheck: state broken after rejections")
	}
	return nil
}

// naiveForward is the independent reference: sort every rotation, take
// the last column, the row of t itself, and the first column F.
func naiveForward(s []byte, term byte) (last []byte, primary int, first []byte) {
	t := append(append([]byte{}, s...), term)
	n := len(t)
	rows := make([]string, n)
	for i := 0; i < n; i++ {
		rows[i] = string(t[i:]) + string(t[:i])
	}
	sort.Strings(rows)
	last = make([]byte, n)
	first = make([]byte, n)
	for i, r := range rows {
		last[i] = r[n-1]
		first[i] = r[0]
		if r == string(t) {
			primary = i
		}
	}
	return last, primary, first
}

// naiveInverse is the independent reference: walk the LF mapping
// computed by its definition (linear scans), strip the trailing term.
func naiveInverse(last []byte, primary int, term byte) []byte {
	n := len(last)
	out := make([]byte, n)
	row := primary
	for i := n - 1; i >= 0; i-- {
		c := last[row]
		out[i] = c
		less, rank := 0, 0
		for j := 0; j < n; j++ {
			if last[j] < c {
				less++
			}
			if j <= row && last[j] == c {
				rank++
			}
		}
		row = less + rank - 1
	}
	return out[:n-1]
}
