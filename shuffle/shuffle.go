// Package shuffle implements the in-place Fisher-Yates shuffle.
package shuffle

import "ontology/rng"

// Shuffle uniformly permutes arr in place. The result depends only on seed,
// so identical (arr, seed) pairs always produce identical permutations.
func Shuffle(arr []int, seed uint64) {
	_ = shuffle(arr, rng.New(seed))
}

// Tracked runs the shuffle and also reports the number of random draws used,
// which is exactly max(n-1, 0) for n elements.
func Tracked(arr []int, seed uint64) (calls int) {
	return shuffle(arr, rng.New(seed))
}

// countingSource wraps a Source and records how many times Intn was called.
type countingSource struct {
	src   rng.Source
	calls int
}

func (c *countingSource) Intn(n int) int {
	c.calls++
	return c.src.Intn(n)
}

// shuffle performs Fisher-Yates: at position i (descending) it chooses j
// uniformly from the unshrunk prefix [0,i], then swaps arr[i] and arr[j].
func shuffle(arr []int, src rng.Source) int {
	cs := &countingSource{src: src}
	for i := len(arr) - 1; i > 0; i-- {
		j := cs.Intn(i + 1)
		arr[i], arr[j] = arr[j], arr[i]
	}
	return cs.calls
}
