package main

import "fmt"

var fails int

func check(name string, ok bool) {
	if ok {
		fmt.Println("OK   " + name)
	} else {
		fmt.Println("FAIL " + name)
		fails++
	}
}

func main() {
	check("skeleton", true)
	fmt.Printf("DONE %d/1\n", 1-fails)
}
