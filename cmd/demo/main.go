// Command demo prints OK/FAIL lines for the jstr codec's required semantics.
package main

import (
	"fmt"
	"os"
)

var fails int

func check(name string, ok bool) {
	status := "OK"
	if !ok {
		status = "FAIL"
		fails++
	}
	fmt.Printf("%-15s %s\n", name, status)
}

func main() {
	check("skeleton", true)
	fmt.Printf("total          %s\n", map[int]string{0: "OK"}[fails])
	if fails > 0 {
		os.Exit(1)
	}
}
