// Package snap holds the snapshot row type, strict-order validation and
// defensive copying. It depends on no other package.
package snap

import "errors"

// Row is one exported table row.
type Row struct {
	Key int64
	Val string
}

// Validation sentinels. While scanning adjacent rows front to back the first
// offending pair decides the category: equal keys are a duplicate, a smaller
// key than the previous row means unsorted.
var (
	ErrDuplicateKey = errors.New("snap: duplicate key")
	ErrNotSorted    = errors.New("snap: snapshot not strictly sorted by key")
)

// Validate checks strict ascending key order. An empty snapshot is valid.
func Validate(rows []Row) error {
	for i := 1; i < len(rows); i++ {
		switch {
		case rows[i].Key == rows[i-1].Key:
			return ErrDuplicateKey
		case rows[i].Key < rows[i-1].Key:
			return ErrNotSorted
		}
	}
	return nil
}

// Clone returns a copy the caller may mutate without aliasing the input.
func Clone(rows []Row) []Row {
	out := make([]Row, len(rows))
	copy(out, rows)
	return out
}
