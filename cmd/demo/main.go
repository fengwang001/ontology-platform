// Command demo prints OK/FAIL lines for each strict-base64 semantic.
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
	check("skeleton", true)
	fmt.Printf("TOTAL %d/1 checks passed\n", 1-fails)
	if fails > 0 {
		os.Exit(1)
	}
}
