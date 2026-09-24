// Command demo exercises the logical and props packages check by check.
package main

import (
	"fmt"
	"os"
)

var fails int

func check(name string, ok bool) {
	if ok {
		fmt.Printf("OK   %s\n", name)
		return
	}
	fmt.Printf("FAIL %s\n", name)
	fails++
}

func main() {
	// Checks are added here as each package part lands.
	fmt.Printf("total: %d failed\n", fails)
	if fails > 0 {
		os.Exit(1)
	}
}
