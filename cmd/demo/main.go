package main

import (
	"fmt"
	"slices"

	"ontology/check"
	"ontology/rng"
	"ontology/shuffle"
)

func wrongShuffle(arr []byte, seed uint64) {
	random := rng.New(seed)
	for i := 0; i < len(arr)-1; i++ {
		j := random.Intn(len(arr))
		arr[i], arr[j] = arr[j], arr[i]
	}
}

func report(name string, ok bool) bool {
	if ok {
		fmt.Printf("OK %s\n", name)
	} else {
		fmt.Printf("FAIL %s\n", name)
	}
	return ok
}

func main() {
	passed := 0
	a, b := []int{1, 2, 3, 4}, []int{1, 2, 3, 4}
	shuffle.Shuffle(a, 99)
	shuffle.Shuffle(b, 99)
	if report("deterministic", slices.Equal(a, b)) {
		passed++
	}

	empty, single := []int{}, []int{7}
	shuffle.Shuffle(empty, 1)
	shuffle.Shuffle(single, 1)
	if report("boundaries", len(empty) == 0 && single[0] == 7) {
		passed++
	}
	if report("multiset", check.VerifyPermutation([]int{1, 2, 2, 3}, 5) == nil) {
		passed++
	}

	before := shuffle.RandomCalls()
	shuffle.Shuffle([]int{1, 2, 3, 4}, 5)
	if report("calls", shuffle.RandomCalls()-before == 3) {
		passed++
	}
	if report("n2-balance", check.CountRatio(check.Distribution(2, 120000, 1)) <= 1.1) {
		passed++
	}
	if report("uniform", check.VerifyUniform(4, 120000, 1, 1.5) == nil) {
		passed++
	}

	counts := map[string]int{}
	for seed := uint64(0); seed < 256; seed++ {
		arr := []byte("ABCD")
		wrongShuffle(arr, seed)
		counts[string(arr)]++
	}
	if report("wrong-biased", check.CountRatio(counts) > 5) {
		passed++
	}

	n2 := check.Distribution(2, 1, 1)
	if report("rng-source", len(n2) == 1) {
		passed++
	}
	fmt.Printf("TOTAL %d/8\n", passed)
}
