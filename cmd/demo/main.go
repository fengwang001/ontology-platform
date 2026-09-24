// Command demo prints OK/FAIL checks for the jstr strict JSON string codec.
package main

import (
	"fmt"
	"os"
)

var failed int

func check(name string, ok bool) {
	mark := "OK  "
	if !ok {
		mark = "FAIL"
		failed++
	}
	fmt.Printf("%s %s\n", mark, name)
}

func main() {
	check("skeleton", true)
	fmt.Printf("== %d check(s), %d failed\n", 1, failed)
	if failed > 0 {
		os.Exit(1)
	}
}
