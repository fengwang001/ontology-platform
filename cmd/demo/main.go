package main

import "fmt"

func ok(name string, passed bool) {
	if passed {
		fmt.Println("OK", name)
	} else {
		fmt.Println("FAIL", name)
	}
}

func main() {
	ok("skeleton", true)
}
