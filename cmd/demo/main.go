// Command demo prints OK/FAIL lines for each jstr semantic guarantee.
package main

import "fmt"

var fails int

func check(name string, ok bool) {
	if ok {
		fmt.Println("OK   " + name)
		return
	}
	fmt.Println("FAIL " + name)
	fails++
}

func main() {
	check("skeleton", true)
	fmt.Printf("total: %d failed\n", fails)
}
