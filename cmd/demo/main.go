package main

import (
	"fmt"
	"math"
	"sync"

	"ontology/arr"
	"ontology/rot"
)

func main() {
	nums := []int{4, 5, 6, 7, 0, 1, 2}
	checks := []bool{
		rot.Search(nums, 0) == 4 && rot.Search(nums, 3) == -1,
		arr.Search(nil, 0) == -1 && arr.Search(nums, 1) == 5,
		arr.Search([]int{3, 1, 2, 0}, 1) == -1,
	}

	index, comparisons := rot.SearchWithComparisons(nums, 2)
	checks = append(checks, index == 6 &&
		float64(comparisons) <= 2*math.Log2(float64(len(nums)))+4)

	var wg sync.WaitGroup
	concurrent := true
	results := make(chan bool, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- rot.Search(nums, 0) == 4
		}()
	}
	wg.Wait()
	close(results)
	for ok := range results {
		concurrent = concurrent && ok
	}
	checks = append(checks, concurrent)

	pass := 0
	total := len(checks)
	for index, ok := range checks {
		if ok {
			pass++
			fmt.Printf("OK check %d\n", index+1)
		} else {
			fmt.Printf("FAIL check %d\n", index+1)
		}
	}
	fmt.Printf("OK total %d/%d\n", pass, total)
}
