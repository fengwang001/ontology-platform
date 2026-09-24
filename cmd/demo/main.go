// Command demo exercises the rle codec and prints one OK/FAIL line
// per required property. It takes no arguments and uses no network.
package main

import (
	"fmt"
	"os"
)

var failed int

func ok(name string, cond bool) {
	status := "OK"
	if !cond {
		status = "FAIL"
		failed++
	}
	fmt.Printf("%-4s %s\n", status, name)
}

func main() {
	// Checks are added here as each part of the codec is implemented.
	fmt.Printf("total: %d FAIL\n", failed)
	if failed > 0 {
		os.Exit(1)
	}
}
