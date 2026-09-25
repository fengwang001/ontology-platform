package check

import (
	"errors"
	"maps"
	"slices"

	"ontology/shuffle"
)

var (
	ErrInvalidInput   = errors.New("check: invalid input")
	ErrNotPermutation = errors.New("check: result is not a multiset permutation")
	ErrNotUniform     = errors.New("check: permutation distribution is not uniform")
)

func counts[T comparable](items []T) map[T]int {
	result := make(map[T]int, len(items))
	for _, value := range items {
		result[value]++
	}
	return result
}

func VerifyMultiset[T comparable](before, after []T) error {
	if len(before) != len(after) || !maps.Equal(counts(before), counts(after)) {
		return ErrNotPermutation
	}
	return nil
}

func VerifyPermutation[T comparable](arr []T, seed uint64) error {
	after := slices.Clone(arr)
	shuffle.Shuffle(after, seed)
	return VerifyMultiset(arr, after)
}

func Distribution(n, trials int, seed uint64) map[string]int {
	counts := make(map[string]int)
	for trial := 0; trial < trials; trial++ {
		arr := make([]byte, n)
		for i := range arr {
			arr[i] = byte('A' + i)
		}
		shuffle.Shuffle(arr, seed+uint64(trial))
		counts[string(arr)]++
	}
	return counts
}

func CountRatio(counts map[string]int) float64 {
	if len(counts) == 0 {
		return 0
	}
	min, max := 0, 0
	for _, count := range counts {
		if min == 0 || count < min {
			min = count
		}
		if count > max {
			max = count
		}
	}
	if min == 0 {
		return float64(max)
	}
	return float64(max) / float64(min)
}

func VerifyUniform(n, trials int, seed uint64, limit float64) error {
	if n < 1 {
		return ErrInvalidInput
	}
	if trials <= 0 {
		return ErrInvalidInput
	}
	counts := Distribution(n, trials, seed)
	if len(counts) != factorial(n) {
		return ErrNotUniform
	}
	if ratio := CountRatio(counts); ratio > limit {
		return ErrNotUniform
	}
	return nil
}

func factorial(n int) int {
	result := 1
	for i := 2; i <= n; i++ {
		result *= i
	}
	return result
}
