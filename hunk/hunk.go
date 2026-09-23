// Package hunk groups shortest edit scripts into context hunks.
package hunk

import "ontology/edit"

// H is one grouped hunk expressed over old/new line indices.
type H struct {
	OldStart int // 0-based index in old lines of hunk start
	OldCount int
	NewStart int // 0-based index in new lines of hunk start
	NewCount int
	Ops      []edit.Op
}

// Group partitions ops into hunks with up to C context lines each side;
// adjacent changes with g <= 2*C unchanged lines are merged.
func Group(ops []edit.Op, c int) []H {
	return nil
}
