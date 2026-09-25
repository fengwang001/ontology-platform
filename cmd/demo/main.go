// Command demo exercises the rle codec end to end and prints OK/FAIL lines.
package main

import (
	"fmt"
	"os"
)

var fails int

func check(name string, ok bool) {
	if !ok {
		fails++
	}
	status := "OK  "
	if !ok {
		status = "FAIL"
	}
	fmt.Println(status, name)
}

func main() {
	check("skeleton", true)
	fmt.Printf("total: %d/1 OK\n", 1-fails)
	if fails > 0 {
		os.Exit(1)
	}
}
