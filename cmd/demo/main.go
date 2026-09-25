package main

import (
	"errors"
	"fmt"
	"math"
	"os"

	"ontology/arr"
	"ontology/rot"
)

var passed, total int

func check(name string, ok bool) {
	total++
	if ok {
		passed++
		fmt.Println("OK", name)
	} else {
		fmt.Println("FAIL", name)
	}
}

func main() {
	nums := []int{4, 5, 6, 7, 0, 1, 2}
	check("rot hit across pivot", rot.Search(nums, 0) == 4)
	check("rot miss", rot.Search(nums, 3) == -1)
	check("rot empty is -1", rot.Search(nil, 1) == -1)
	check("rot no rotation", rot.Search([]int{0, 1, 2, 4, 5, 6, 7}, 5) == 4)
	idx, err := arr.Search(nums, 7)
	check("arr valid delegates", idx == 3 && err == nil)
	idx, err = arr.Search(nil, 1)
	check("arr empty is -1,nil", idx == -1 && err == nil)
	_, err = arr.Search([]int{2, 1, 3}, 1)
	check("arr rejects bad rotation", errors.Is(err, arr.ErrNotRotation))
	big := make([]int, 100000)
	for i := range big {
		big[i] = (i + 377) % 100000
	}
	rot.ResetComparisons()
	rot.Search(big, 99999)
	check("comparisons within bound", rot.Comparisons() <= int64(2*math.Log2(100000))+4)
	fmt.Printf("OK total %d/%d\n", passed, total)
	if passed != total {
		os.Exit(1)
	}
}
