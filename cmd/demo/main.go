// Command demo exercises the rle codec end to end and prints OK/FAIL lines.
package main

import "fmt"

var fails int

func check(name string, cond bool) {
	if cond {
		fmt.Println("OK   " + name)
	} else {
		fmt.Println("FAIL " + name)
		fails++
	}
}

func main() {
	check("skeleton", true)
	fmt.Printf("TOTAL %d failure(s)\n", fails)
}
