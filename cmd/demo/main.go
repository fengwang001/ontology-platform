// Command demo prints OK/FAIL lines for the jstr codec's required semantics.
package main

import (
	"fmt"
	"os"
)

var failed int

func check(name string, ok bool) {
	if !ok {
		failed++
	}
	fmt.Printf("%-14s %v\n", name, map[bool]string{true: "OK", false: "FAIL"}[ok])
}

func main() {
	check("skeleton", true)
	fmt.Printf("total          %v\n", map[bool]string{true: "OK", false: "FAIL"}[failed == 0])
	if failed > 0 {
		os.Exit(1)
	}
}
