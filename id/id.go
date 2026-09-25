// Package id defines the element index type used with uf and
// re-exports the sentinel errors callers should test with errors.Is.
package id

import "ontology/uf"

// ID is the index of an element in a union-find structure.
type ID int

// ErrBadIndex reports an element index outside [0, n).
var ErrBadIndex = uf.ErrBadIndex
