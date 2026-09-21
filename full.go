package ontology

// FullDistance computes the exact Levenshtein distance between a and b
// with no threshold, in runes. It exists so tests and the demo can verify
// that Compare returns the exact distance whenever it does not exceed k.
// It runs in O(len(a)*len(b)) time and O(min(len)) memory; do not use it
// on very large inputs.
func FullDistance(a, b string, foldCase bool) (int, error) {
	ra, err := decodeRunes(a, 'a')
	if err != nil {
		return 0, err
	}
	rb, err := decodeRunes(b, 'b')
	if err != nil {
		return 0, err
	}
	if foldCase {
		foldRunes(ra)
		foldRunes(rb)
	}
	if len(ra) < len(rb) {
		ra, rb = rb, ra
	}
	prev := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	curr := make([]int, len(rb)+1)
	for i, ca := range ra {
		curr[0] = i + 1
		for j, cb := range rb {
			cost := 0
			if ca != cb {
				cost = 1
			}
			curr[j+1] = min(prev[j+1]+1, curr[j]+1, prev[j]+cost)
		}
		prev, curr = curr, prev
	}
	return prev[len(rb)], nil
}
