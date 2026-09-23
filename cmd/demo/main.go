package main

import (
	"fmt"
	"os"

	"ontology/vec"
)

var failures int

func judge(name string, ok bool) {
	if ok {
		fmt.Printf("OK   %s\n", name)
		return
	}
	fmt.Printf("FAIL %s\n", name)
	failures++
}

func main() {
	a := vec.Vector{1, 2, 3}
	b := vec.Vector{4, 5, 6}
	dot, _ := vec.Dot(a, b)
	judge("vec dot/euclid basic", dot == 32)

	if failures > 0 {
		fmt.Printf("TOTAL %d FAIL\n", failures)
		os.Exit(1)
	}
	fmt.Println("TOTAL 0 FAIL")
}
