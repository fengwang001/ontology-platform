package main

import "fmt"

var fails int

func check(name string, ok bool) {
	if ok {
		fmt.Println("OK   " + name)
	} else {
		fails++
		fmt.Println("FAIL " + name)
	}
}

func main() {
	check("skeleton", true)
	if fails != 0 {
		fmt.Printf("TOTAL FAIL %d\n", fails)
		return
	}
	fmt.Println("TOTAL OK")
}
