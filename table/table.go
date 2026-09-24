// Package table builds the KMP failure table for a pattern: for every
// prefix of the pattern, the length of the longest proper prefix that
// is also a suffix of that prefix.
package table

// Table is the failure table of one pattern. The zero value is invalid;
// construct it with Build.
type Table struct {
	f []int // f[k] = border length of the prefix of length k+1
}

// Build returns the failure table for pattern p, which must be non-empty.
func Build(p string) Table {
	f := make([]int, len(p))
	for i := 1; i < len(p); i++ {
		k := f[i-1]
		for k > 0 && p[i] != p[k] {
			k = f[k-1]
		}
		if p[i] == p[k] {
			k++
		}
		f[i] = k
	}
	return Table{f: f}
}

// Len returns the number of table entries, i.e. the pattern length.
func (t Table) Len() int { return len(t.f) }

// Fallback returns the matched length to resume with after a mismatch (or
// a full match) at matched length j: the table value f[j-1] itself.
// j must be in [1, Len].
func (t Table) Fallback(j int) int { return t.f[j-1] }

// At returns the table value for the prefix of length k+1.
func (t Table) At(k int) int { return t.f[k] }
