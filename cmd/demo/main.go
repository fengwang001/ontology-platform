// Command demo prints OK/FAIL lines for each semantic guarantee of the
// ontology/rle package. It takes no arguments and exits 0 on success.
package main

import (
	"fmt"
	"os"
)

var passed, failed int

func check(name string, ok bool) {
	if ok {
		passed++
		fmt.Println("OK   " + name)
		return
	}
	failed++
	fmt.Println("FAIL " + name)
}

func main() {
	// Checks are added here as each part of runs/rle is implemented.
	fmt.Printf("TOTAL %d/%d\n", passed, passed+failed)
	if failed > 0 {
		os.Exit(1)
	}
}
