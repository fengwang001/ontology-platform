package main

import "fmt"

var fails int

func step(name string, ok bool) {
	if ok {
		fmt.Println("OK   " + name)
	} else {
		fails++
		fmt.Println("FAIL " + name)
	}
}

func main() {
	step("skeleton", true)
	if fails == 0 {
		fmt.Println("ALL OK")
	} else {
		fmt.Printf("%d FAIL\n", fails)
	}
}
