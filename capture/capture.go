// Package capture reports which outer declarations a scope references.
package capture

import (
	"ontology/name"
	"ontology/resolve"
	"ontology/scope"
)

// Item is one captured outer declaration and the depth it lives at.
type Item struct {
	Decl      name.Decl
	FromDepth int
}

// Of lists the distinct outer declarations referenced from within s.
// A reference counts only if it was recorded in s itself and resolved
// to a declaration at a strictly smaller depth; repeats collapse to one.
func Of(s *scope.Scope, refs []resolve.Record) []Item {
	seen := make(map[name.Decl]bool)
	var out []Item
	for _, r := range refs {
		if r.Scope != s || r.Result.Depth >= s.Depth() {
			continue
		}
		if seen[r.Result.Decl] {
			continue
		}
		seen[r.Result.Decl] = true
		out = append(out, Item{Decl: r.Result.Decl, FromDepth: r.Result.Depth})
	}
	return out
}
