package lcp

import (
	"bytes"
	"math/rand"
	"testing"

	"ontology/suffix"
)

// buildFor is a shorthand that constructs SA then the LCP table.
func buildFor(s []byte) *Table { return Build(s, suffix.Build(s)) }

// TestComparisonCountLinearBound proves Kasai is O(n), not pairwise O(n^2):
// across sizes 100..10000 (worst shape all-'a' included) total byte
// comparisons never exceed 2*n. The counter is read white-box here; no
// exported API exposes it.
func TestComparisonCountLinearBound(t *testing.T) {
	rng := rand.New(rand.NewSource(99))
	for _, n := range []int{100, 257, 1000, 4096, 10000} {
		cases := [][]byte{
			bytes.Repeat([]byte{'a'}, n), // worst shape: maximal shared prefixes
		}
		for alpha := 2; alpha <= 4; alpha++ { // loop-generated random byte strings
			b := make([]byte, n)
			for i := range b {
				b[i] = byte('a' + rng.Intn(alpha))
			}
			cases = append(cases, b)
		}
		for ci, s := range cases {
			tab := buildFor(s)
			if tab.compares > int64(2*n) {
				t.Fatalf("n=%d case=%d: %d comparisons > 2n=%d",
					n, ci, tab.compares, 2*n)
			}
		}
	}
}

// TestKasaiArray checks LCP values and the longest repeated substring on a few
// fixed byte strings against hand-derived truth.
func TestKasaiArray(t *testing.T) {
	for _, c := range []struct {
		s        []byte
		sa, lcpv []int
		st, ln   int
	}{
		{[]byte("ababab"), []int{4, 2, 0, 5, 3, 1}, []int{2, 4, 0, 1, 3}, 0, 4},
		{[]byte("banana"), []int{5, 3, 1, 0, 4, 2}, []int{1, 3, 0, 0, 2}, 1, 3},
		{[]byte("aaaa"), []int{3, 2, 1, 0}, []int{1, 2, 3}, 0, 3},
		{[]byte("a"), []int{0}, []int{}, 0, 0},
	} {
		tab := buildFor(c.s)
		got := tab.LCP()
		if len(got) != len(c.lcpv) {
			t.Fatalf("LCP len %d, want %d", len(got), len(c.lcpv))
		}
		for k := range got {
			if got[k] != c.lcpv[k] {
				t.Fatalf("LCP(%q)[%d] = %d, want %d", c.s, k, got[k], c.lcpv[k])
			}
		}
		st, ln := tab.Longest()
		if st != c.st || ln != c.ln {
			t.Fatalf("Longest(%q) = (%d,%d), want (%d,%d)", c.s, st, ln, c.st, c.ln)
		}
	}
}

// TestLeftmostTie uses random strings: the longest gap's start must be the
// minimum occurrence, never just SA[argmax].
func TestLeftmostTie(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for range 100 {
		n := 1 + rng.Intn(60)
		s := make([]byte, n)
		for i := range s {
			s[i] = byte('a' + rng.Intn(3))
		}
		tab := buildFor(s)
		st, ln := tab.Longest()
		ws, wl := naive(s)
		if st != ws || ln != wl {
			t.Fatalf("(%q) = (%d,%d), naive (%d,%d)", s, st, ln, ws, wl)
		}
	}
}

// naive enumerates every suffix pair; ties resolve to the leftmost start.
func naive(s []byte) (st, ln int) {
	for i := 0; i < len(s); i++ {
		for j := i + 1; j < len(s); j++ {
			h := 0
			for i+h < len(s) && j+h < len(s) && s[i+h] == s[j+h] {
				h++
			}
			if l := min(i, j); h > ln || (h == ln && h > 0 && l < st) {
				st, ln = l, h
			}
		}
	}
	return
}
