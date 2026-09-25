// Package avg defines the value rule of a single pairwise exchange.
// It depends on nothing else in the module.
package avg

// Split returns the new values for two nodes after one exchange, in the
// same order as the inputs: the first return value belongs to (id1, v1),
// the second to (id2, v2).
//
// Even total: both nodes get total/2.
// Odd total: the node with the lexicographically smaller ID gets
// (total-1)/2, the other gets (total+1)/2, so the sum is always
// conserved exactly (nv1+nv2 == v1+v2). For odd totals total-1 and
// total+1 are even, so the integer divisions are exact for negative
// values too.
func Split(id1 string, v1 int, id2 string, v2 int) (nv1, nv2 int) {
	total := v1 + v2
	if total%2 == 0 {
		return total / 2, total / 2
	}
	lo, hi := (total-1)/2, (total+1)/2
	if id1 < id2 {
		return lo, hi
	}
	return hi, lo
}
