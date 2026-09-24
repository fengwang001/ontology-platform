package ontology

import "strconv"

// Keys returns n deterministic, distinct keys ("key-0" .. "key-n-1").
// Tests and the demo share this generator so results are reproducible.
func Keys(n int) []string {
	keys := make([]string, n)
	for i := range keys {
		keys[i] = "key-" + strconv.Itoa(i)
	}
	return keys
}
