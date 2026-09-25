package main

import (
	"fmt"

	"ontology/rot"
)

func main() {
	pass, total := 0, 0
	check := func(name string, ok bool) {
		total++
		if ok {
			pass++
			fmt.Printf("OK   %s\n", name)
		} else {
			fmt.Printf("FAIL %s\n", name)
		}
	}
	rotated := []int{4, 5, 6, 7, 0, 1, 2}
	check("rotated hit 0 -> 4", rot.Search(rotated, 0) == 4)
	check("rotated miss 3 -> -1", rot.Search(rotated, 3) == -1)
	check("empty -> -1", rot.Search(nil, 1) == -1)
	check("single hit and miss", rot.Search([]int{5}, 5) == 0 && rot.Search([]int{5}, 9) == -1)
	if pass == total {
		fmt.Printf("OK   total %d/%d passed\n", pass, total)
	} else {
		fmt.Printf("FAIL total %d/%d passed\n", pass, total)
	}
}
