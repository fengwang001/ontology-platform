package main

import (
	"fmt"
	"os"

	"ontology/ient"
)

var fails int

func check(name string, ok bool) {
	if ok {
		fmt.Println("OK  " + name)
	} else {
		fmt.Println("FAIL " + name)
		fails++
	}
}

func main() {
	check("log-bound checked items (ient)", ient.New().SelfCheck() == nil)
	if fails > 0 {
		os.Exit(1)
	}
}
