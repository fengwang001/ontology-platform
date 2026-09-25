// Command demo exercises sel.KthSmallest and prints OK/FAIL per check.
package main

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"os"

	"ontology/check"
	"ontology/ord"
	"ontology/sel"
)

var failed bool

func report(name string, ok bool) {
	status := "OK"
	if !ok {
		status = "FAIL"
		failed = true
	}
	fmt.Printf("%s %s\n", status, name)
}

func kth(arr []int, k int) int {
	v, _ := sel.KthSmallest(arr, k)
	return v
}

func main() {
	report("kth([3 2 1 5 4], 2) == 3", kth([]int{3, 2, 1, 5, 4}, 2) == 3)
	report("kth([3 2 3 1 2], 3) == 3 (duplicates)", kth([]int{3, 2, 3, 1, 2}, 3) == 3)
	arr := []int{9, 4, 7, 1, 8}
	report("k=0 is min, k=n-1 is max", kth(arr, 0) == 1 && kth(arr, 4) == 9)
	_, err := sel.KthSmallest([]int{}, 0)
	report("empty slice -> ErrEmpty", errors.Is(err, ord.ErrEmpty))
	_, err = sel.KthSmallest([]int{1, 2}, 2)
	report("k out of range -> ErrBadK", errors.Is(err, ord.ErrBadK))
	data := rand.Perm(1000)
	report("matches sorted reference", kth(append([]int(nil), data...), 517) == check.KthSmallestRef(data, 517))
	sel.ResetCompares()
	kth(rand.Perm(100000), 50000)
	report("avg compares <= 4n (n=100000)", sel.Compares() <= 4*100000)
	if failed {
		fmt.Println("FAIL some checks failed")
		os.Exit(1)
	}
	fmt.Println("OK all checks passed")
}
