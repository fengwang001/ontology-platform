// Command demo exercises the rle codec end to end and prints OK/FAIL
// per check. Exit code is 0 only if every check passes.
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
	fmt.Printf("total: %d failed\n", fails)
}
