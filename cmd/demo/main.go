// Command demo runs the package-level acceptance checks for the suffix-array task.
package main

import (
	"bytes"
	"errors"
	"fmt"
	"slices"
	"sort"
	"sync"

	"ontology/api"
	"ontology/lcp"
	"ontology/suffix"
)

func ok(cond bool, name string) {
	if cond {
		fmt.Println("OK", name)
	} else {
		fmt.Println("FAIL", name)
	}
}

func naiveSA(s []byte) []int {
	idx := make([]int, len(s))
	for i := range idx {
		idx[i] = i
	}
	sort.Slice(idx, func(a, b int) bool {
		return bytes.Compare(s[idx[a]:], s[idx[b]:]) < 0
	})
	return idx
}

func isPerm(sa []int) bool {
	seen := make([]bool, len(sa))
	for _, v := range sa {
		if v < 0 || v >= len(sa) || seen[v] {
			return false
		}
		seen[v] = true
	}
	return true
}

func main() {
	var x api.Index
	if err := x.New([]byte("ababab")); err != nil {
		panic(err)
	}
	sa, _ := x.SA()
	lv, _ := x.LCP()
	st, ln, _ := x.LongestRepeated()
	ok(slices.Equal(sa, []int{4, 2, 0, 5, 3, 1}), "SA(ababab)=[4 2 0 5 3 1]")
	ok(slices.Equal(lv, []int{2, 4, 0, 1, 3}), "LCP(ababab)=[2 4 0 1 3]")
	ok(st == 0 && ln == 4, `longest repeated = "abab" len 4 at 0`)
	ok(slices.Equal(sa, naiveSA([]byte("ababab"))), "SA matches bytes.Compare naive reference")
	ok(isPerm(sa), "SA is a permutation of 0..n-1")

	var q api.Index
	_, e0 := q.SA()
	e1 := q.New(nil)
	e2 := q.New([]byte{0xff})
	ok(errors.Is(e0, api.ErrNotBuilt) && errors.Is(e1, api.ErrEmpty) &&
		errors.Is(e2, api.ErrInvalidUTF8), "four distinct decidable errors")
	_, still := q.SA()
	_, e3 := x.At(99)
	distinct := e0 != e1 && e1 != e2 && e2 != e3 && errors.Is(still, api.ErrNotBuilt) &&
		errors.Is(e3, api.ErrOutOfRange)
	ok(distinct, "rejected ops leave no trace; index stays usable")

	big := bytes.Repeat([]byte{'a'}, 10000)
	ok(lcp.Build(big, suffix.Build(big)).LinearBound(), "LCP comparisons <= 2n at n=10000 (all a)")

	const N = 64
	var wg sync.WaitGroup
	starts, lens := make([]int, N), make([]int, N)
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			starts[i], lens[i], _ = x.LongestRepeated()
		}(i)
	}
	wg.Wait()
	same := true
	for i := 1; i < N; i++ {
		same = same && starts[i] == starts[0] && lens[i] == lens[0]
	}
	ok(same, "concurrent LongestRepeated gives field-wise identical results")
}
