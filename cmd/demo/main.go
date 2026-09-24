// Command demo prints OK/FAIL checks for the jstr strict JSON string codec.
package main

import "fmt"

var fails int

func check(name string, ok bool) {
	status := "OK"
	if !ok {
		status = "FAIL"
		fails++
	}
	fmt.Println(status, name)
}

func main() {
	// Checks are added here as each package is implemented.
	fmt.Printf("total: %d failed\n", fails)
	if fails > 0 {
		panic("demo checks failed")
	}
}
