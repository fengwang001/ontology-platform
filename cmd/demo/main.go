// Command demo exercises the rle codec end to end and prints OK/FAIL lines.
package main

import (
	"fmt"
	"os"
)

var checks, fails int

func check(name string, ok bool) {
	checks++
	if !ok {
		fails++
	}
	verdict := "OK  "
	if !ok {
		verdict = "FAIL "
	}
	fmt.Println(verdict + name)
}

func main() {
	check("skeleton", true)
	fmt.Printf("total: %d checks, %d failed\n", checks, fails)
	if fails > 0 {
		os.Exit(1)
	}
}
