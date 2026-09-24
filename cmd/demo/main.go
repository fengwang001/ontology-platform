// Command demo exercises the rle codec and prints one OK/FAIL line per check.
package main

import (
	"fmt"
	"os"
)

var failures int

func check(name string, ok bool) {
	if ok {
		fmt.Println("OK  " + name)
		return
	}
	fmt.Println("FAIL " + name)
	failures++
}

func main() {
	// Checks are added here as each package is implemented.
	fmt.Printf("TOTAL %d failure(s)\n", failures)
	if failures > 0 {
		os.Exit(1)
	}
}
