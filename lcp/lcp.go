// Package lcp computes the LCP array from a suffix array (Kasai's algorithm)
// and derives the longest repeated substring. It depends on package suffix
// only through the SA passed in by callers.
package lcp

// Table is the result of building the LCP array. All fields are unexported;
// in particular the character-comparison counter is not readable through the
// package's API (white-box tests in this package read the field directly).
type Table struct {
	n        int
	sa       []int
	lcp      []int
	start    int
	length   int
	compares int64 // total byte comparisons made while computing LCP
}

// Build computes the LCP array of s given its suffix array sa. The returned
// table is immutable: accessors return copies, so concurrent readers are safe.
func Build(s []byte, sa []int) *Table {
	n := len(sa)
	t := &Table{n: n, sa: append([]int(nil), sa...)}
	if n == 0 {
		return t
	}
	rank := make([]int, n) // inverse suffix array: suffix start -> SA position
	for k, i := range sa {
		rank[i] = k
	}
	t.lcp = make([]int, n-1)

	// Kasai: h never decreases by more than one across iterations, which is
	// why the total number of byte comparisons is bounded by 2*n.
	h := 0
	var compares int64
	for i := 0; i < n; i++ {
		k := rank[i]
		if k == n-1 {
			h = 0
			continue
		}
		j := sa[k+1]
		for i+h < n && j+h < n {
			compares++ // one pairwise byte comparison
			if s[i+h] != s[j+h] {
				break
			}
			h++
		}
		t.lcp[k] = h
		// Longest repeated substring: maximum gap LCP; on a tie keep the
		// leftmost occurrence, i.e. the smaller of the two suffix starts,
		// and across equal gaps the overall smallest start.
		if h > t.length {
			t.length = h
			a, b := sa[k], sa[k+1]
			t.start = a
			if b < a {
				t.start = b
			}
		} else if h == t.length && h > 0 {
			a, b := sa[k], sa[k+1]
			left := a
			if b < a {
				left = b
			}
			if left < t.start {
				t.start = left
			}
		}
		if h > 0 {
			h--
		}
	}
	t.compares = compares
	return t
}

// SA returns a copy of the suffix array.
func (t *Table) SA() []int { return append([]int(nil), t.sa...) }

// LCP returns a copy of the LCP array (length n-1; empty when n <= 1).
func (t *Table) LCP() []int { return append([]int(nil), t.lcp...) }

// Longest returns the leftmost start and the length of the longest substring
// occurring at least twice (0, 0 when no character repeats).
func (t *Table) Longest() (start, length int) { return t.start, t.length }

// LinearBound reports whether the LCP computation performed at most 2*n byte
// comparisons, without exposing the counter value itself.
func (t *Table) LinearBound() bool { return t.compares <= int64(2*t.n) }
