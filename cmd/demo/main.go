package main

import "fmt"

var fails int

func check(name string, ok bool) {
	if ok {
		fmt.Println("OK  ", name)
		return
	}
	fmt.Println("FAIL", name)
	fails++
}

func main() {
	check("skeleton", true)
	if fails > 0 {
		fmt.Printf("TOTAL FAIL %d\n", fails)
		return
	}
	fmt.Println("TOTAL OK")
}
