package main

import "fmt"

var fails int

func ok(name string, cond bool) {
	if cond {
		fmt.Println("OK   " + name)
	} else {
		fmt.Println("FAIL " + name)
		fails++
	}
}

func main() {
	ok("skeleton", true)
	fmt.Printf("total: %d fail\n", fails)
	if fails > 0 {
		fmt.Println("DEMO FAILED")
	}
}
