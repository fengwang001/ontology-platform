package main

import "fmt"

var failed bool

func line(name string, ok bool) {
	tag := "OK"
	if !ok {
		tag, failed = "FAIL", true
	}
	fmt.Printf("%s %s\n", tag, name)
}

func main() {
	line("skeleton", true)
	if failed {
		panic("demo failed")
	}
}
