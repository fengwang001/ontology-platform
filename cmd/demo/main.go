// Command demo exercises the logical and props packages end to end.
package main

import (
	"fmt"
	"os"
)

var failures int

func check(name string, cond bool) {
	status := "OK"
	if !cond {
		status = "FAIL"
		failures++
	}
	fmt.Printf("%s %s\n", status, name)
}

func main() {
	// Checks are added here as each package is implemented.
	fmt.Printf("total: %d failure(s)\n", failures)
	if failures > 0 {
		os.Exit(1)
	}
}
