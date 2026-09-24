// Command demo exercises the balanced-parentheses packages.
package main

import (
	"fmt"
	"os"

	"ontology/longest"
)

var failed bool

func report(name string, ok bool) {
	tag := "OK"
	if !ok {
		tag = "FAIL"
		failed = true
	}
	fmt.Printf("%s: %s\n", tag, name)
}

func main() {
	const limit = 200000
	s := longest.New(limit)

	// Demo entries are added alongside each package implementation.
	_ = s

	if failed {
		os.Exit(1)
	}
}
