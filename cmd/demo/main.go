package main

import (
	"fmt"
	"os"
)

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
		fmt.Println("TOTAL FAIL")
		os.Exit(1)
	}
	fmt.Println("TOTAL OK")
}
