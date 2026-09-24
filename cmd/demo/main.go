// Command demo prints OK/FAIL lines for each semantic guarantee of jstr.
package main

import (
	"fmt"
	"os"
)

var fails int

func check(name string, ok bool) {
	tag := "OK   "
	if !ok {
		tag = "FAIL "
		fails++
	}
	fmt.Println(tag + name)
}

func main() {
	// Checks are added below as each part of jstr/esc is implemented.
	fmt.Printf("total: %d check(s) failed\n", fails)
	if fails > 0 {
		os.Exit(1)
	}
}
