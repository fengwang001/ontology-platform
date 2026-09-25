// Package check provides the naive reference matcher used to
// validate the Rabin-Karp implementation in package match.
package check

import "ontology/hash"

// Naive returns all start positions (ascending, overlaps included)
// of pattern in text by direct substring comparison at each index.
func Naive(text, pattern string) []int {
	out := []int{}
	for i := 0; i+len(pattern) <= len(text); i++ {
		if text[i:i+len(pattern)] == pattern {
			out = append(out, i)
		}
	}
	return out
}

// hashHits is the buggy "hash hit == match" matcher: it reports every
// rolling-hash hit without char-by-char verification, so collisions
// become false positives. It exists only to pin the collision test.
func hashHits(text, pat string, base, mod uint64) []int {
	ph, _ := hash.New(base, mod)
	wh, _ := hash.New(base, mod)
	for i := 0; i < len(pat); i++ {
		ph.Append(pat[i])
	}
	hits := []int{}
	for i := 0; i < len(text); i++ {
		wh.Append(text[i])
		if wh.Len() > len(pat) {
			wh.Remove(text[i-len(pat)])
		}
		if wh.Len() == len(pat) && wh.Value() == ph.Value() {
			hits = append(hits, i-len(pat)+1)
		}
	}
	return hits
}
