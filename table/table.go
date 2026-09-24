// Package table builds the failure table for a pattern: for every
// prefix length, the length of the longest proper prefix of the
// pattern that is also a suffix of that prefix.
package table

// Table is the compiled failure table of one pattern. The zero value
// is not usable; build it with Compile. It is immutable after Compile
// and safe for concurrent reads.
type Table struct {
	pattern string
	fail    []int // fail[k] for matched length k in [0, len(pattern)]
}

// Compile precomputes the failure table for pattern. fail[0] and
// fail[1] are 0; on a mismatch at matched length k, the matched
// length falls back to fail[k] (never fail[k]-1).
func Compile(pattern string) Table {
	fail := make([]int, len(pattern)+1)
	for i := 2; i <= len(pattern); i++ {
		k := fail[i-1]
		for k > 0 && pattern[k] != pattern[i-1] {
			k = fail[k]
		}
		if pattern[k] == pattern[i-1] {
			k++
		}
		fail[i] = k
	}
	return Table{pattern: pattern, fail: fail}
}

// Len returns the pattern length.
func (t Table) Len() int { return len(t.pattern) }

// At returns the fallback matched length for matched length k.
// It always satisfies 0 <= At(k) < k for k >= 1.
func (t Table) At(k int) int { return t.fail[k] }

// Byte returns the i-th byte of the pattern.
func (t Table) Byte(i int) byte { return t.pattern[i] }

// Pattern returns the compiled pattern string.
func (t Table) Pattern() string { return t.pattern }
