// Package ws classifies whitespace that may become trailing whitespace.
package ws

// IsTrailing recognizes only space and tab per the normalization rule.
func IsTrailing(b byte) bool { return b == ' ' || b == '\t' }

// Run describes a byte-relative run of trailing whitespace candidates.
type Run struct {
	Start int
	End   int
}

// Valid reports whether r contains at least one byte.
func (r Run) Valid() bool { return r.Start < r.End }

// Len reports the number of buffered candidate bytes.
func (r Run) Len() int {
	if !r.Valid() {
		return 0
	}
	return r.End - r.Start
}

// Append extends r with one byte at offset.
func (r Run) Append(offset int) Run {
	if !r.Valid() {
		return Run{offset, offset + 1}
	}
	r.End = offset + 1
	return r
}

// Clear drops a pending run.
func (r *Run) Clear() { *r = Run{} }
