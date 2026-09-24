package main

import "fmt"

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
		panic("demo checks failed")
	}
}
