package main

import "fmt"

var fails int

func check(name string, ok bool) {
	if ok {
		fmt.Println("OK  ", name)
		return
	}
	fails++
	fmt.Println("FAIL", name)
}

func main() {
	check("skeleton", true)
	if fails == 0 {
		fmt.Println("ALL OK")
		return
	}
	fmt.Printf("%d FAIL\n", fails)
}
