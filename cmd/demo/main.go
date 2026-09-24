// Command demo exercises the properties parser and store end to end.
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
	fmt.Printf("%-24s %s\n", name, status)
}

func main() {
	check("separators", false)       // TODO
	check("comments", false)         // TODO
	check("continuations", false)    // TODO
	check("escapes+unicode", false)  // TODO
	check("duplicate-order", false)  // TODO
	check("store-roundtrip", false)  // TODO
	check("random-roundtrip", false) // TODO
	check("byte-counter", false)     // TODO
	fmt.Printf("total: 8 checks, %d failed\n", fails)
	if fails > 0 {
		os.Exit(1)
	}
}
