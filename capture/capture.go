// Package capture computes which outer-scope declarations a scope
// referenced, deduplicated, one entry per captured declaration.
package capture

import "ontology/resolve"

// Outer filters the resolved references made inside a scope at depth
// atDepth down to those that landed in a strictly outer scope
// (hit.Depth < atDepth). A name declared in the same scope — even at a
// later position via a forward declaration — resolves locally and is
// therefore never a capture. Each distinct hit is reported once.
func Outer(hits []resolve.Hit, atDepth int) []resolve.Hit {
	seen := make(map[resolve.Hit]bool)
	var out []resolve.Hit
	for _, h := range hits {
		if h.Depth >= atDepth || seen[h] {
			continue
		}
		seen[h] = true
		out = append(out, h)
	}
	return out
}
