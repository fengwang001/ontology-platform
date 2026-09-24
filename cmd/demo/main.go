package main

import (
	"fmt"
	"os"
)

var failed bool

func check(name string, ok bool) {
	if ok {
		fmt.Printf("OK   %s\n", name)
	} else {
		failed = true
		fmt.Printf("FAIL %s\n", name)
	}
}

func main() {
	check("skeleton", true)
	if failed {
		os.Exit(1)
	}
}
