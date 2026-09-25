// Package check provides uniformity statistics for the shuffle package.
package check

import (
	"errors"
	"math/rand"

	"ontology/shuffle"
)

var (
	// ErrEmptySample is returned when no trials were performed.
	ErrEmptySample = errors.New("check: empty sample")
	// ErrZeroBucket is returned when some permutation never occurred.
	ErrZeroBucket = errors.New("check: zero-count bucket (non-uniform)")
	// ErrNonUniform is returned when the max/min count ratio exceeds limit.
	ErrNonUniform = errors.New("check: counts outside uniformity limit")
)

// CountPerms shuffles base n elements with trials distinct seeds and returns
// how often each resulting permutation occurred, keyed by a base-n code.
func CountPerms(n, trials int) (map[int]int, error) {
	if trials <= 0 {
		return nil, ErrEmptySample
	}
	counts := make(map[int]int)
	arr := make([]int, n)
	for t := 0; t < trials; t++ {
		for i := range arr {
			arr[i] = i
		}
		shuffle.Shuffle(arr, uint64(t)+1)
		counts[encode(arr, n)]++
	}
	return counts, nil
}

// CountPermsBuggy applies the faulty "full-range pick" shuffle and returns its
// permutation counts. On each of n-1 passes it swaps position i with a
// different index taken from the whole [0,n), so many permutations are
// unreachable and the distribution is far from uniform.
func CountPermsBuggy(n, trials int) (map[int]int, error) {
	if trials <= 0 {
		return nil, ErrEmptySample
	}
	counts := make(map[int]int)
	arr := make([]int, n)
	for t := 0; t < trials; t++ {
		for i := range arr {
			arr[i] = i
		}
		r := rand.New(rand.NewSource(int64(t) + 1))
		for i := 0; i < n-1; i++ {
			j := r.Intn(n - 1)
			if j >= i {
				j++
			}
			arr[i], arr[j] = arr[j], arr[i]
		}
		counts[encode(arr, n)]++
	}
	return counts, nil
}

// Ratio returns maxCount/minCount across all n! expected buckets. It returns
// ErrZeroBucket if any expected bucket has count zero.
func Ratio(counts map[int]int, buckets int) (float64, error) {
	if buckets <= 0 || len(counts) == 0 {
		return 0, ErrEmptySample
	}
	min, max := -1, 0
	for k := 0; k < buckets; k++ {
		v := counts[k]
		if v == 0 {
			return 0, ErrZeroBucket
		}
		if min < 0 || v < min {
			min = v
		}
		if v > max {
			max = v
		}
	}
	return float64(max) / float64(min), nil
}

// AssertUniform verifies counts over all buckets satisfy ratio <= limit.
func AssertUniform(counts map[int]int, buckets int, limit float64) error {
	ratio, err := Ratio(counts, buckets)
	if err != nil {
		return err
	}
	if ratio > limit {
		return ErrNonUniform
	}
	return nil
}

func encode(arr []int, base int) int {
	code := 0
	for _, v := range arr {
		code = code*base + v
	}
	return code
}
