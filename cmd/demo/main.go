package main

import (
	"fmt"
	"os"

	"ontology/etime"
)

var failed bool

func check(name string, ok bool) {
	if ok {
		fmt.Printf("OK   %s\n", name)
	} else {
		fmt.Printf("FAIL %s\n", name)
		failed = true
	}
}

func main() {
	check("skeleton", true)
	if failed {
		os.Exit(1)
	}
}
