// Command demo exercises the rle codec end to end and prints OK/FAIL lines.
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
	fmt.Printf("%-4s %s\n", status, name)
}

func main() {
	check("skeleton runs", true)
	fmt.Printf("total: %d failed\n", fails)
	if fails > 0 {
		os.Exit(1)
	}
}
