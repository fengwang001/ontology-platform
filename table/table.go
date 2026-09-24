package table

import "errors"

var ErrEmptyPattern = errors.New("empty pattern")

// Table is the KMP prefix-function table for one compiled pattern.
type Table struct {
	pattern string
	values  []int
}

// New builds a mismatch table from pattern.
func New(pattern string) (Table, error) {
	if pattern == "" {
		return Table{}, ErrEmptyPattern
	}

	values := make([]int, len(pattern))
	for i := 1; i < len(pattern); i++ {
		matched := values[i-1]
		for matched > 0 && pattern[i] != pattern[matched] {
			matched = values[matched-1]
		}
		if pattern[i] == pattern[matched] {
			matched++
		}
		values[i] = matched
	}

	return Table{pattern: pattern, values: values}, nil
}

// Pattern returns the compiled pattern.
func (t Table) Pattern() string {
	return t.pattern
}

// Len returns the pattern length.
func (t Table) Len() int {
	return len(t.pattern)
}

// At returns the table entry at zero-based pattern position.
func (t Table) At(position int) int {
	if position < 0 || position >= len(t.values) {
		return -1
	}
	return t.values[position]
}
