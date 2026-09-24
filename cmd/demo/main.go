package main

import (
	"errors"
	"fmt"

	"ontology/vec"
)

func main() {
	checks := 0
	total := 0
	judge := func(name string, ok bool) {
		total++
		if ok {
			checks++
			fmt.Println("OK: " + name)
		} else {
			fmt.Println("FAIL: " + name)
		}
	}

	judge("vec 维度不符返回 ErrDimMismatch", errors.Is(vec.CheckDim(2, 3), vec.ErrDimMismatch))

	fmt.Printf("TOTAL: %d/%d checks passed\n", checks, total)
}
