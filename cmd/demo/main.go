// Command demo prints OK/FAIL checks for the jstr strict JSON string codec.
package main

import "fmt"

var failed int

func check(name string, ok bool) {
	status := "OK"
	if !ok {
		status = "FAIL"
		failed++
	}
	fmt.Printf("%s %s\n", status, name)
}

func main() {
	check("skeleton", true)
	fmt.Printf("total: %d failed\n", failed)
}
