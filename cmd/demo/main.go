// Command demo prints OK/FAIL checks for the rle codec.
package main

import "fmt"

var fails int

func check(name string, ok bool) {
	status := "OK"
	if !ok {
		status = "FAIL"
		fails++
	}
	fmt.Printf("%s %s\n", status, name)
}

func main() {
	check("skeleton", true)
	fmt.Printf("total: %d failure(s)\n", fails)
}
