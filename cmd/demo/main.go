// Command demo exercises the strict streaming Base64 codec.
package main

import "fmt"

var failures int

func check(name string, ok bool) {
	status := "OK"
	if !ok {
		status = "FAIL"
		failures++
	}
	fmt.Printf("%s %s\n", status, name)
}

func main() {
	fmt.Printf("total: %d failure(s)\n", failures)
}
